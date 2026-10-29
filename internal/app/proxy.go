package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/elazarl/goproxy"
	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/net/http2"
	"mihomo-capture/internal/gateway"
)

type captureBuffer struct {
	mu       sync.Mutex
	b        []byte
	size     int64
	limit    int
	ended    bool
	expected int64
	err      string
}

func (b *captureBuffer) add(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.size += int64(len(p))
	if len(b.b) < b.limit {
		n := min(len(p), b.limit-len(b.b))
		b.b = append(b.b, p[:n]...)
	}
}
func (b *captureBuffer) body(encoding string) Body {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := Body{Size: b.size, Truncated: b.size > int64(len(b.b)), ContentEncoding: encoding}
	if utf8.Valid(b.b) && !bytes.Contains(b.b, []byte{0}) {
		out.Encoding = "utf8"
		out.Data = string(b.b)
	} else {
		out.Encoding = "base64"
		out.Data = base64.StdEncoding.EncodeToString(b.b)
	}
	return out
}

type bodyTap struct {
	io.ReadCloser
	b    *captureBuffer
	done func()
	once sync.Once
}

func (b *bodyTap) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.b.add(p[:n])
	if err != nil {
		b.b.mu.Lock()
		b.b.ended = err == io.EOF
		if err != io.EOF {
			b.b.err = err.Error()
		}
		b.b.mu.Unlock()
	}
	if err != nil && b.done != nil {
		b.once.Do(b.done)
	}
	return n, err
}
func (b *bodyTap) Close() error {
	err := b.ReadCloser.Close()
	if b.done != nil {
		b.once.Do(b.done)
	}
	return err
}

type flowState struct {
	t        ticket
	r        *Record
	req, res *captureBuffer
	mu       sync.Mutex
	release  func()
}
type ingress struct {
	ticket    ticket
	transport *http.Transport
}
type ingressKey struct{}

type Proxy struct {
	a            *App
	ca           *tls.Certificate
	cache        *lru.Cache[string, *tls.Certificate]
	engine       *goproxy.ProxyHttpServer
	slots        chan struct{}
	mu           sync.Mutex
	connections  map[net.Conn]struct{}
	roots        *x509.CertPool
	captureSlots chan struct{}
}

func NewProxy(a *App, ca *tls.Certificate) (*Proxy, error) {
	cache, _ := lru.New[string, *tls.Certificate](512)
	p := &Proxy{a: a, ca: ca, cache: cache, slots: make(chan struct{}, a.Config.MaxConnections), connections: map[net.Conn]struct{}{}}
	p.captureSlots = make(chan struct{}, 64)
	if a.Config.UpstreamCAFile != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		b, err := os.ReadFile(a.Config.UpstreamCAFile)
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(b) {
			return nil, errors.New("upstream_ca_file contains no valid certificate")
		}
		p.roots = roots
	}
	p.engine = goproxy.NewProxyHttpServer()
	p.engine.KeepAcceptEncoding = true
	// goproxy defaults to insecure upstream TLS; this service always verifies it.
	p.engine.Tr = &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true, DisableCompression: true}
	p.engine.OnRequest().DoFunc(p.request)
	p.engine.OnResponse().DoFunc(p.response)
	return p, nil
}

func (p *Proxy) certificate(host string) (*tls.Certificate, error) {
	if c, ok := p.cache.Get(host); ok {
		return c, nil
	}
	c, err := goproxy.TLSConfigFromCA(p.ca)(host, &goproxy.ProxyCtx{Proxy: p.engine})
	if err != nil {
		return nil, err
	}
	cert := &c.Certificates[0]
	p.cache.Add(host, cert)
	return cert, nil
}

func (p *Proxy) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for c := range p.connections {
		_ = c.Close()
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only the gateway's proxy nodes may name a lane and the chosen policy.
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		http.Error(w, "loopback proxy node required", 403)
		return
	}
	u, pw, ok := proxyBasic(r.Header.Get("Proxy-Authorization"))
	if !ok || u != gateway.ProxyUser || subtle.ConstantTimeCompare([]byte(pw), []byte(p.a.Config.ProxyToken)) != 1 {
		http.Error(w, "proxy authentication required", 407)
		return
	}
	if r.Method != "CONNECT" {
		http.Error(w, "CONNECT required", 405)
		return
	}
	lane, policy := r.Header.Get(gateway.LaneHeader), r.Header.Get(gateway.PolicyHeader)
	if (lane != gateway.LaneCapture && lane != gateway.LaneDaily) || policy == "" {
		http.Error(w, "lane and policy headers required", 400)
		return
	}
	// The ticket belongs to this connection: requests it carries after the
	// capture changes are not attributed to the new capture.
	var t ticket
	if lane == gateway.LaneCapture {
		t = p.a.captureTicket()
	}
	if _, _, err := net.SplitHostPort(r.Host); err != nil {
		http.Error(w, "target must include a port", 400)
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "connection limit reached", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	up, err := p.a.gate.DialContext(ctx, policy, r.Host)
	cancel()
	if err != nil {
		http.Error(w, "upstream route unavailable", 502)
		return
	}
	defer up.Close()
	client, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	rawClient := client
	p.mu.Lock()
	p.connections[client] = struct{}{}
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.connections, rawClient); p.mu.Unlock() }()
	if _, err = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	client = &bufferConn{Conn: client, r: rw.Reader}
	targetHost, _, _ := net.SplitHostPort(r.Host)
	if t.device == "" && !p.a.shouldDecrypt(targetHost) {
		relay(client, up)
		return
	}
	// Race the first byte in both directions. Server-first protocols such as SSH
	// keep their original banner; HTTP and TLS are client-first.
	client, up, serverFirst, err := firstBytes(client, up)
	if err != nil {
		return
	}
	if serverFirst {
		relay(client, up)
		return
	}
	br := bufio.NewReader(client)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	client = &bufferConn{Conn: client, r: br}
	scheme := "http"
	if first[0] == 22 {
		var protocols []string
		var helloSeen bool
		var consumed bytes.Buffer
		_ = client.SetReadDeadline(time.Now().Add(15 * time.Second))
		dry := &helloConn{Conn: client, r: io.TeeReader(io.LimitReader(client, 65536), &consumed)}
		_ = tls.Server(dry, &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			helloSeen = true
			protocols = append([]string{}, h.SupportedProtos...)
			return nil, io.EOF
		}}).Handshake()
		_ = client.SetReadDeadline(time.Time{})
		client = &bufferConn{client, io.MultiReader(bytes.NewReader(consumed.Bytes()), client)}
		httpALPN := len(protocols) == 0
		for _, v := range protocols {
			httpALPN = httpALPN || v == "h2" || v == "http/1.1"
		}
		if !helloSeen || !httpALPN {
			relay(client, up)
			return
		}
		tlsClient := tls.Server(client, &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}, GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = targetHost
			}
			return p.certificate(name)
		}})
		_ = client.SetDeadline(time.Now().Add(15 * time.Second))
		err = tlsClient.Handshake()
		_ = client.SetDeadline(time.Time{})
		if err != nil {
			return
		}
		client = tlsClient
		scheme = "https"
		if tlsClient.ConnectionState().NegotiatedProtocol != "h2" {
			var isHTTP bool
			client, isHTTP = classifyHTTP(client)
			if !isHTTP {
				tlsUp := tls.Client(up, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: targetHost, RootCAs: p.roots})
				if err = tlsUp.Handshake(); err == nil {
					relay(client, tlsUp)
				}
				return
			}
		}
	} else {
		var isHTTP bool
		client, isHTTP = classifyHTTP(client)
		if !isHTTP {
			relay(client, up)
			return
		}
	}
	var dialMu sync.Mutex
	firstDial := true
	tr := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, DisableCompression: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: p.roots}, TLSHandshakeTimeout: 15 * time.Second, MaxIdleConns: 4, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		dialMu.Lock()
		defer dialMu.Unlock()
		if firstDial {
			firstDial = false
			if target == r.Host {
				return up, nil
			}
			_ = up.Close()
		}
		return p.a.gate.DialContext(ctx, policy, target)
	}}
	defer tr.CloseIdleConnections()
	in := &ingress{t, tr}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req = req.WithContext(context.WithValue(req.Context(), ingressKey{}, in))
		if req.URL.Scheme == "" {
			req.URL.Scheme = scheme
		}
		if req.URL.Host == "" {
			req.URL.Host = req.Host
		}
		if req.URL.Host == "" {
			req.URL.Host = r.Host
		}
		if req.Method == "CONNECT" {
			http.Error(w, "nested CONNECT is unsupported", 400)
			return
		}
		if strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
			defer client.Close()
		}
		p.engine.ServeHTTP(w, req)
	})
	if tc, ok := client.(*tls.Conn); ok && tc.ConnectionState().NegotiatedProtocol == "h2" {
		(&http2.Server{MaxConcurrentStreams: 128, IdleTimeout: 90 * time.Second}).ServeConn(client, &http2.ServeConnOpts{Handler: handler})
		return
	}
	// h2c prior knowledge uses the same standard HTTP/2 implementation.
	peek := bufio.NewReader(client)
	preface, _ := peek.Peek(3)
	client = &bufferConn{Conn: client, r: peek}
	if string(preface) == "PRI" {
		(&http2.Server{MaxConcurrentStreams: 128, IdleTimeout: 90 * time.Second}).ServeConn(client, &http2.ServeConnOpts{Handler: handler})
		return
	}
	tc := &trackedConn{Conn: client, done: make(chan struct{})}
	defer tc.Close()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	_ = srv.Serve(&singleListener{c: tc})
}

func proxyBasic(v string) (string, string, bool) {
	if !strings.HasPrefix(v, "Basic ") {
		return "", "", false
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, "Basic "))
	if err != nil {
		return "", "", false
	}
	u, p, ok := strings.Cut(string(b), ":")
	return u, p, ok
}

func (p *Proxy) request(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	in, ok := r.Context().Value(ingressKey{}).(*ingress)
	if !ok {
		return r, goproxy.NewResponse(r, "text/plain", 502, "missing ingress context")
	}
	ctx.RoundTripper = goproxy.RoundTripperFunc(func(req *http.Request, pc *goproxy.ProxyCtx) (*http.Response, error) {
		res, err := in.transport.RoundTrip(req)
		if err != nil {
			pc.Error = err
			return goproxy.NewResponse(req, "text/plain", 502, "upstream request failed"), nil
		}
		return res, nil
	})
	r.Header.Del("Proxy-Authorization")
	f := &flowState{t: in.ticket, req: &captureBuffer{limit: p.a.Config.MaxBodyBytes}, res: &captureBuffer{limit: p.a.Config.MaxBodyBytes}}
	f.r = &Record{ID: newID(), At: time.Now().UTC(), Method: r.Method, URL: r.URL.String(), Host: r.URL.Hostname(), Path: r.URL.RequestURI(), Request: Message{Headers: r.Header.Clone()}}
	f.req.expected = r.ContentLength
	if r.Body == nil || r.Body == http.NoBody {
		f.req.ended = true
	}
	if f.t.device != "" {
		select {
		case p.captureSlots <- struct{}{}:
			f.release = func() { <-p.captureSlots }
		default:
			f.req.limit = 0
			f.res.limit = 0
			f.r.Error = "capture capacity exceeded; bodies and frames omitted"
		}
	}
	if r.Host != "" {
		f.r.Request.Headers.Set("Host", r.Host)
	}
	ctx.UserData = f
	if f.t.device != "" && r.Body != nil {
		r.Body = &bodyTap{ReadCloser: r.Body, b: f.req}
	}
	return r, nil
}

func (p *Proxy) response(res *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
	f, ok := ctx.UserData.(*flowState)
	if !ok || f.t.device == "" {
		return res
	}
	if res == nil {
		f.r.Error = "upstream request failed"
		f.r.Complete = true
		p.a.record(f.t, f.r)
		return res
	}
	f.r.Status = res.StatusCode
	if ctx.Error != nil {
		f.r.Error = ctx.Error.Error()
	}
	f.r.Response.Headers = res.Header.Clone()
	finish := func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.release != nil {
			f.release()
			f.release = nil
		}
		f.r.Complete = true
		f.r.DurationMS = time.Since(f.r.At).Milliseconds()
		f.r.Request.Body = inspectBody(f.req.body(f.r.Request.Headers.Get("Content-Encoding")), p.a.Config.MaxBodyBytes)
		f.r.Response.Body = inspectBody(f.res.body(res.Header.Get("Content-Encoding")), p.a.Config.MaxBodyBytes)
		f.r.Response.Trailers = res.Trailer.Clone()
		f.req.mu.Lock()
		requestComplete := f.req.ended || (f.req.expected >= 0 && f.req.size == f.req.expected)
		requestError := f.req.err
		f.req.mu.Unlock()
		f.res.mu.Lock()
		responseError := f.res.err
		f.res.mu.Unlock()
		f.r.Replayable = !f.r.Request.Body.Truncated && requestComplete
		if requestError != "" {
			f.r.Error = requestError
		}
		if responseError != "" {
			f.r.Error = responseError
		}
		if f.r.Replayable {
			f.r.Curl = curl(f.r)
		}
		p.a.record(f.t, f.r)
	}
	if res.StatusCode == 101 {
		if rw, ok := res.Body.(io.ReadWriteCloser); ok {
			f.r.DurationMS = time.Since(f.r.At).Milliseconds()
			f.r.Request.Body = f.req.body(f.r.Request.Headers.Get("Content-Encoding"))
			f.r.Replayable = !f.r.Request.Body.Truncated
			if f.r.Replayable {
				f.r.Curl = curl(f.r)
			}
			p.a.record(f.t, f.r)
			res.Body = newWebsocketTap(rw, p.a, f, finish)
			return res
		}
	}
	// Store headers immediately, so active SSE/downloads are visible.
	p.a.record(f.t, f.r)
	res.Body = &bodyTap{ReadCloser: res.Body, b: f.res, done: finish}
	return res
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func curl(r *Record) string {
	args := []string{"curl", "--globoff", "--request", shellQuote(r.Method)}
	keys := make([]string, 0, len(r.Request.Headers))
	for k := range r.Request.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Proxy-Authorization") {
			continue
		}
		for _, v := range r.Request.Headers[k] {
			args = append(args, "--header", shellQuote(k+": "+v))
		}
	}
	if r.Request.Body.Size > 0 {
		b := []byte(r.Request.Body.Data)
		if r.Request.Body.Encoding == "base64" {
			b, _ = base64.StdEncoding.DecodeString(r.Request.Body.Data)
		}
		payload := base64.StdEncoding.EncodeToString(b)
		return "printf %s " + shellQuote(payload) + " | base64 --decode | " + strings.Join(append(args, "--data-binary", "@-", "--url", shellQuote(r.URL)), " ")
	}
	return strings.Join(append(args, "--url", shellQuote(r.URL)), " ")
}

type bufferConn struct {
	net.Conn
	r io.Reader
}

func (c *bufferConn) CloseWrite() error {
	if v, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return v.CloseWrite()
	}
	return nil
}

type helloConn struct {
	net.Conn
	r io.Reader
}

func (c *helloConn) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *helloConn) Write([]byte) (int, error)  { return 0, io.EOF }

func (c *bufferConn) Read(b []byte) (int, error) { return c.r.Read(b) }

type trackedConn struct {
	net.Conn
	once sync.Once
	done chan struct{}
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() { close(c.done); err = c.Conn.Close() })
	return err
}

type singleListener struct {
	c    *trackedConn
	used bool
}

func (l *singleListener) Accept() (net.Conn, error) {
	if !l.used {
		l.used = true
		return l.c, nil
	}
	<-l.c.done
	return nil, net.ErrClosed
}
func (l *singleListener) Close() error   { return nil }
func (l *singleListener) Addr() net.Addr { return l.c.LocalAddr() }

func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		if c, ok := a.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		if c, ok := b.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

func firstBytes(client, up net.Conn) (net.Conn, net.Conn, bool, error) {
	cr, ur := bufio.NewReader(client), bufio.NewReader(up)
	type result struct {
		server bool
		err    error
	}
	ch := make(chan result, 2)
	go func() { _, err := cr.Peek(1); ch <- result{false, err} }()
	go func() { _, err := ur.Peek(1); ch <- result{true, err} }()
	first := <-ch
	if first.server {
		_ = client.SetReadDeadline(time.Now())
	} else {
		_ = up.SetReadDeadline(time.Now())
	}
	<-ch
	_ = client.SetReadDeadline(time.Time{})
	_ = up.SetReadDeadline(time.Time{})
	wrap := func(c net.Conn, r *bufio.Reader) net.Conn {
		b, _ := r.Peek(r.Buffered())
		return &bufferConn{c, io.MultiReader(bytes.NewReader(bytes.Clone(b)), c)}
	}
	return wrap(client, cr), wrap(up, ur), first.server, first.err
}

func classifyHTTP(c net.Conn) (net.Conn, bool) {
	r := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var prefix []byte
	isHTTP := false
	for len(prefix) < 32 {
		b, err := r.ReadByte()
		if err != nil {
			break
		}
		prefix = append(prefix, b)
		if b == ' ' {
			isHTTP = len(prefix) > 1
			break
		}
		if !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", rune(b)) {
			break
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	buffered, _ := r.Peek(r.Buffered())
	prefix = append(prefix, buffered...)
	return &bufferConn{c, io.MultiReader(bytes.NewReader(prefix), c)}, isHTTP
}
