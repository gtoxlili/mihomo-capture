package module

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/itchyny/gojq"
	"golang.org/x/net/http/httpguts"
)

type Options struct {
	// StorePath holds $persistentStore across restarts.
	StorePath string
	// Dial leaves through the gateway policy of that name, for $httpClient's
	// policy option. Nil when there is no gateway.
	Dial func(ctx context.Context, policy, address string) (net.Conn, error)
}

// Engine applies modules to requests and responses. Every failure leaves the
// message as it was and is noted in the module's log.
type Engine struct {
	store    *Store
	dial     func(ctx context.Context, policy, address string) (net.Conn, error)
	slots    chan struct{} // concurrent script runs; later runs queue within their timeout
	direct   *http.Transport
	insecure *http.Transport
	mu       sync.Mutex
	logs     map[string]*ring
}

func NewEngine(o Options) (*Engine, error) {
	store, err := OpenStore(o.StorePath)
	if err != nil {
		return nil, err
	}
	transport := func(insecure bool) *http.Transport {
		return &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxIdleConns: 16, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure}}
	}
	return &Engine{store: store, dial: o.Dial, slots: make(chan struct{}, max(4, runtime.NumCPU())), direct: transport(false), insecure: transport(true), logs: map[string]*ring{}}, nil
}

// Close writes $persistentStore and drops idle $httpClient connections.
func (e *Engine) Close() error {
	e.direct.CloseIdleConnections()
	e.insecure.CloseIdleConnections()
	return e.store.Close()
}

// Forget drops a removed module's log and its scripts' keyless storage.
func (e *Engine) Forget(id string) {
	e.mu.Lock()
	delete(e.logs, id)
	e.mu.Unlock()
	e.store.deletePrefix(scriptKey(id, ""))
}

// Exchange is one request on its way through the modules that act on it.
type Exchange struct {
	ID string // $request.id, shared by the request and the response scripts
	// Modules are those whose hostnames include the request's host, in order.
	Modules []*Module
	// Abort is set when a script asked to close the connection.
	Abort   bool
	applied []string
	local   bool // answered by the request phase; no upstream response follows
	sent    requestView
}

// Applied names the modules whose rules took effect, in order.
func (x *Exchange) Applied() []string { return x.applied }

func (x *Exchange) mark(m *Module) {
	for _, n := range x.applied {
		if n == m.Name() {
			return
		}
	}
	x.applied = append(x.applied, m.Name())
}

// requestView is the request as it went upstream, which response scripts see.
type requestView struct {
	url, method, host string
	header            http.Header
}

// Request runs the request phase in order: header rewrite, URL rewrite, map
// local, body rewrite, then the first matching http-request script. A non-nil response answers the request without going upstream.
func (e *Engine) Request(x *Exchange, r *http.Request) *http.Response {
	if len(x.Modules) == 0 {
		return nil
	}
	ctx := r.Context()
	target := r.URL.String()
	for _, m := range x.Modules {
		for _, h := range m.headers[request] {
			if e.matches(m, h.pattern, target, "header_rewrite") {
				applyRequestHeader(r, h)
				x.mark(m)
			}
		}
	}
rewrite:
	for _, m := range x.Modules {
		for _, u := range m.urls {
			if !e.matches(m, u.pattern, target, "url_rewrite") {
				continue
			}
			// The first matching URL rewrite decides.
			x.mark(m)
			if u.kind == "reject" {
				return x.answer(r, http.StatusNotFound, http.Header{}, nil)
			}
			next, _, err := u.pattern.replace(target, u.replacement)
			if err != nil {
				e.note(m, "warn", "url_rewrite", target, "%v", err)
				break rewrite
			}
			if u.kind != "header" {
				code, _ := strconv.Atoi(u.kind)
				return x.answer(r, code, http.Header{"Location": {next}}, nil)
			}
			if to, ok := requestURL(next); ok {
				r.URL, r.Host = to, to.Host
				target = r.URL.String()
			} else {
				e.note(m, "warn", "url_rewrite", target, "rewritten URL %q is not an http, https or ws URL; rewrite discarded", next)
			}
			break rewrite
		}
	}
	for _, m := range x.Modules {
		for _, l := range m.locals {
			if e.matches(m, l.pattern, target, "map_local") {
				x.mark(m)
				return x.answer(r, l.status, l.header.Clone(), l.body)
			}
		}
	}
	rules, sm, s := e.bodyPhase(x, request, target)
	var b *body
	if len(rules) > 0 || s != nil && s.RequiresBody {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			// An upgrade carries frames, not a body.
			rules, s = nil, s.withoutBody()
		} else {
			b = readBody(r.Body, r.Header.Get("Content-Encoding"), bodyLimit(rules, s))
			e.rewriteBody(ctx, x, b, rules, target)
		}
	}
	if s != nil {
		in := call{kind: HTTPRequest, id: x.ID, req: viewOf(r)}
		if body, ok := e.scriptBody(sm, s, b, target); ok {
			in.body, in.hasBody = body, len(body) > 0
		}
		if out, ok := e.run(ctx, sm, s, in); ok {
			x.mark(sm)
			if out.response != nil {
				return x.answer(r, out.response.status, out.response.header, out.response.body)
			}
			if out.abort {
				x.Abort = true
				return nil
			}
			if out.url != "" {
				if u, ok := requestURL(out.url); ok {
					r.URL = u // Unlike URL rewrite, the Host header is kept.
				} else {
					e.note(sm, "warn", s.Name, target, "$done url %q is not an http, https or ws URL; ignored", out.url)
				}
			}
			if out.header != nil {
				if host := out.header.Get("Host"); host != "" {
					r.Host = host
				}
				out.header.Del("Host")
				r.Header = out.header
			}
			if out.hasBody {
				if !s.RequiresBody {
					e.note(sm, "warn", s.Name, target, "$done body ignored: the script does not set requires_body")
				} else if b != nil {
					b.plain, b.changed = out.body, true
				}
			}
		}
	}
	if b != nil {
		var n int64
		var decoded bool
		r.Body, n, decoded = b.reader(r.Body)
		if b.changed {
			r.ContentLength, r.TransferEncoding = n, nil
			r.Header.Del("Content-Length")
			r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b.plain)), nil }
			if decoded {
				r.Header.Del("Content-Encoding")
			}
		}
	}
	x.sent = viewOf(r)
	return nil
}

// Response runs the response phase: header rewrite, body rewrite, then the
// first matching http-response script. A response the request phase produced
// locally is not processed again.
func (e *Engine) Response(x *Exchange, res *http.Response) {
	if len(x.Modules) == 0 || x.local {
		return
	}
	ctx := context.Background()
	if res.Request != nil {
		ctx = res.Request.Context()
	}
	target := x.sent.url
	for _, m := range x.Modules {
		for _, h := range m.headers[response] {
			if e.matches(m, h.pattern, target, "header_rewrite") {
				applyHeader(res.Header, h)
				x.mark(m)
			}
		}
	}
	rules, sm, s := e.bodyPhase(x, response, target)
	media, _, _ := strings.Cut(res.Header.Get("Content-Type"), ";")
	// No body to rewrite, or one that never ends.
	if x.sent.method == http.MethodHead || res.StatusCode < 200 || res.StatusCode == 204 || res.StatusCode == 304 || strings.EqualFold(strings.TrimSpace(media), "text/event-stream") {
		rules, s = nil, s.withoutBody()
	}
	var b *body
	if len(rules) > 0 || s != nil && s.RequiresBody {
		b = readBody(res.Body, res.Header.Get("Content-Encoding"), bodyLimit(rules, s))
		e.rewriteBody(ctx, x, b, rules, target)
	}
	if s != nil {
		in := call{kind: HTTPResponse, id: x.ID, req: x.sent, status: res.StatusCode, header: res.Header}
		if body, ok := e.scriptBody(sm, s, b, target); ok {
			in.body, in.hasBody = body, len(body) > 0
		}
		if out, ok := e.run(ctx, sm, s, in); ok {
			x.mark(sm)
			if out.abort {
				x.Abort = true
				return
			}
			if out.status != 0 {
				if out.status >= 200 && out.status <= 999 {
					res.StatusCode, res.Status = out.status, fmt.Sprintf("%d %s", out.status, http.StatusText(out.status))
				} else {
					e.note(sm, "warn", s.Name, target, "$done status %d ignored: it must be 200 to 999", out.status)
				}
			}
			if out.header != nil {
				res.Header = out.header
			}
			if out.hasBody {
				if !s.RequiresBody {
					e.note(sm, "warn", s.Name, target, "$done body ignored: the script does not set requires_body")
				} else if b != nil {
					b.plain, b.changed = out.body, true
				}
			}
		}
	}
	if b != nil {
		var n int64
		var decoded bool
		res.Body, n, decoded = b.reader(res.Body)
		if b.changed {
			res.ContentLength, res.TransferEncoding = n, nil
			res.Header.Set("Content-Length", strconv.FormatInt(n, 10))
			if decoded {
				res.Header.Del("Content-Encoding")
			}
		}
	}
}

type matchedBody struct {
	m    *Module
	rule bodyRule
}

// bodyPhase returns every matching body rewrite and the one script that runs,
// the first that matches: one script modifies a message.
func (e *Engine) bodyPhase(x *Exchange, dir int, target string) ([]matchedBody, *Module, *script) {
	var rules []matchedBody
	for _, m := range x.Modules {
		for _, b := range m.bodies[dir] {
			if e.matches(m, b.pattern, target, "body_rewrite") {
				rules = append(rules, matchedBody{m, b})
			}
		}
	}
	for _, m := range x.Modules {
		for _, s := range m.scripts[dir] {
			if e.matches(m, s.pattern, target, s.Name) {
				return rules, m, s
			}
		}
	}
	return rules, nil, nil
}

// withoutBody is the script as it runs on a message without a body to give it.
func (s *script) withoutBody() *script {
	if s == nil || !s.RequiresBody {
		return s
	}
	c := *s
	c.RequiresBody = false
	return &c
}

func bodyLimit(rules []matchedBody, s *script) int64 {
	var n int64
	if len(rules) > 0 {
		n = RewriteBodySize
	}
	if s != nil && s.RequiresBody {
		n = max(n, s.maxBytes)
	}
	return n
}

func (e *Engine) rewriteBody(ctx context.Context, x *Exchange, b *body, rules []matchedBody, target string) {
	for _, r := range rules {
		if b.plain == nil {
			e.note(r.m, "warn", "body_rewrite", target, "body left unmodified: %s", b.problem)
			continue
		}
		if r.rule.jq != nil {
			out, err := applyJQ(ctx, r.rule.jq, b.plain)
			if err != nil {
				e.note(r.m, "warn", "body_rewrite", target, "jq rule skipped: %v", err)
				continue
			}
			x.mark(r.m)
			if out != nil {
				b.plain, b.changed = out, true
			}
			continue
		}
		if !utf8.Valid(b.plain) {
			e.note(r.m, "warn", "body_rewrite", target, "regex rule skipped: the body is not UTF-8 text")
			continue
		}
		s, changed := string(b.plain), false
		for _, p := range r.rule.pairs {
			next, ok, err := p.regex.replace(s, p.replacement)
			if err != nil {
				e.note(r.m, "warn", "body_rewrite", target, "%v", err)
				continue
			}
			s, changed = next, changed || ok
		}
		x.mark(r.m)
		if changed {
			b.plain, b.changed = []byte(s), true
		}
	}
}

// applyJQ returns the filter's first output, or nil when it produces none,
// which keeps the original body.
func applyJQ(ctx context.Context, code *gojq.Code, body []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber() // integers beyond 2^53 keep their digits
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, fmt.Errorf("the body is not JSON: %v", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("the body is not one JSON value")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, ok := code.RunWithContext(ctx, v).Next()
	if !ok {
		return nil, nil
	}
	if err, ok := out.(error); ok {
		return nil, err
	}
	return gojq.Marshal(out)
}

func (e *Engine) scriptBody(m *Module, s *script, b *body, target string) ([]byte, bool) {
	if !s.RequiresBody || b == nil {
		return nil, false
	}
	if b.plain == nil {
		e.note(m, "warn", s.Name, target, "script ran without the body: %s", b.problem)
		return nil, false
	}
	if int64(len(b.plain)) > s.maxBytes {
		e.note(m, "warn", s.Name, target, "script ran without the body: %d bytes exceed max_size", len(b.plain))
		return nil, false
	}
	return b.plain, true
}

func (e *Engine) matches(m *Module, p *pattern, target, source string) bool {
	ok, err := p.match(target)
	if err != nil {
		e.note(m, "warn", source, target, "%v", err)
	}
	return ok
}

func requestURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return nil, false
	}
	return u, true
}

func viewOf(r *http.Request) requestView {
	return requestView{url: r.URL.String(), method: r.Method, host: r.Host, header: r.Header.Clone()}
}

func (x *Exchange) answer(r *http.Request, status int, h http.Header, b []byte) *http.Response {
	x.local = true
	h.Del("Content-Length")
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: h, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Request: r}
}

// applyRequestHeader treats Host, which Go keeps outside the header map, as
// the single-valued header it is on the wire.
func applyRequestHeader(r *http.Request, h headerRule) {
	if h.name != "Host" {
		applyHeader(r.Header, h)
		return
	}
	switch h.action {
	case "header-add", "header-replace":
		r.Host = h.value
	case "header-replace-regex":
		if v, ok, err := h.regex.replace(r.Host, h.replacement); err == nil && ok && v != "" {
			r.Host = v
		}
	}
}

func applyHeader(header http.Header, h headerRule) {
	switch h.action {
	case "header-add":
		header.Add(h.name, h.value)
	case "header-del":
		header.Del(h.name)
	case "header-replace":
		if len(header.Values(h.name)) > 0 {
			header.Set(h.name, h.value)
		}
	case "header-replace-regex":
		values := header.Values(h.name)
		for i, v := range values {
			if next, ok, err := h.regex.replace(v, h.replacement); err == nil && ok && httpguts.ValidHeaderFieldValue(next) {
				values[i] = next
			}
		}
	}
}

// body is a message body read for rewriting. When it cannot be rewritten,
// problem says why and the original bytes are forwarded untouched.
type body struct {
	raw      []byte
	rest     io.Reader // unread remainder, after raw
	plain    []byte    // decoded content, nil when unavailable
	problem  string
	decoded  bool // plain differs from raw by a content encoding
	changed  bool
	original io.Closer
}

func readBody(rc io.ReadCloser, encoding string, limit int64) *body {
	b := &body{original: rc}
	if rc == nil || rc == http.NoBody {
		b.plain = []byte{}
		return b
	}
	raw, err := io.ReadAll(io.LimitReader(rc, limit+1))
	b.raw = raw
	switch {
	case err != nil:
		b.rest, b.problem = rc, "reading the body failed: "+err.Error()
		return b
	case int64(len(raw)) > limit:
		b.rest, b.problem = rc, fmt.Sprintf("the body exceeds %d bytes", limit)
		return b
	}
	plain, complete, err := Decode(raw, encoding, int(limit))
	switch {
	case err != nil:
		b.problem = err.Error()
	case !complete:
		b.problem = fmt.Sprintf("the decoded body exceeds %d bytes", limit)
	default:
		b.plain, b.decoded = plain, strings.TrimSpace(encoding) != ""
	}
	return b
}

// reader is the body to forward: the rewritten content, or the original
// bytes followed by whatever was not read.
func (b *body) reader(rc io.ReadCloser) (io.ReadCloser, int64, bool) {
	if b.changed {
		if b.original != nil {
			_ = b.original.Close()
		}
		return io.NopCloser(bytes.NewReader(b.plain)), int64(len(b.plain)), b.decoded
	}
	if rc == nil || rc == http.NoBody {
		return rc, 0, false
	}
	r := io.Reader(bytes.NewReader(b.raw))
	if b.rest != nil {
		r = io.MultiReader(r, b.rest)
	}
	return struct {
		io.Reader
		io.Closer
	}{r, rc}, int64(len(b.raw)), false
}

// note adds to a module's log.
func (e *Engine) note(m *Module, level, source, target, format string, args ...any) {
	entry := LogEntry{At: time.Now().UTC(), Level: level, Source: source, URL: target, Message: fmt.Sprintf(format, args...)}
	if len(entry.Message) > maxLogMessage {
		entry.Message = entry.Message[:maxLogMessage] + "…"
	}
	if len(entry.URL) > 2048 {
		entry.URL = entry.URL[:2048] + "…"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.logs[m.ID]
	if r == nil {
		r = &ring{}
		e.logs[m.ID] = r
	}
	r.add(entry)
}

// Logs returns a module's recent log, newest first.
func (e *Engine) Logs(id string) []LogEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r := e.logs[id]; r != nil {
		return r.list()
	}
	return []LogEntry{}
}

type LogEntry struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`  // info, warn, error or notify
	Source  string    `json:"source"` // a script name, or the rule list
	URL     string    `json:"url,omitempty"`
	Message string    `json:"message"`
}

const (
	logSize       = 200
	maxLogMessage = 4 << 10
)

type ring struct {
	items [logSize]LogEntry
	next  int
	n     int
}

func (r *ring) add(e LogEntry) {
	r.items[r.next] = e
	r.next = (r.next + 1) % logSize
	r.n = min(r.n+1, logSize)
}

func (r *ring) list() []LogEntry {
	out := make([]LogEntry, 0, r.n)
	for i := 1; i <= r.n; i++ {
		out = append(out, r.items[(r.next-i+logSize)%logSize])
	}
	return out
}
