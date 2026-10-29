package app

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gtoxlili/mihomo-capture/internal/gateway"
)

const selectedMAC = "00:11:22:33:44:55"
const otherMAC = "00:11:22:33:44:66"

type fixture struct {
	gate   *fakeGateway
	a      *App
	proxy  *Proxy
	server *httptest.Server
	ca     *tls.Certificate
	leaf   tls.Certificate
	roots  *x509.CertPool
	dir    string
}

// fakeGateway stands in for mihomo at the service boundary: it keeps the
// published selection and leaves through DIRECT by dialing origins itself.
// The stock-core path is covered by the gateway package.
type fakeGateway struct {
	mu        sync.Mutex
	selection gateway.Selection
	history   []gateway.Selection
	fail      error
}

func (g *fakeGateway) Select(_ context.Context, s gateway.Selection) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fail != nil {
		return g.fail
	}
	g.selection = s
	g.history = append(g.history, s)
	return nil
}
func (g *fakeGateway) published() gateway.Selection {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.selection
}
func (g *fakeGateway) Status(context.Context) (gateway.Status, error) { return gateway.Status{}, nil }
func (g *fakeGateway) Clear(context.Context) error                    { return nil }
func (g *fakeGateway) Close()                                         {}
func (g *fakeGateway) DialContext(ctx context.Context, policy, target string) (net.Conn, error) {
	if policy != "direct" {
		return nil, fmt.Errorf("unknown fixture policy %q", policy)
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", target)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "oc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Fixture CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	ca := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: root}
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: root.NotBefore, NotAfter: root.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(dir, "root.pem")
	if err = os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	gate := &fakeGateway{}
	c := Config{APIListen: "127.0.0.1:0", ProxyListen: "127.0.0.1:19091", DataDir: filepath.Join(dir, "data"), LAN: "192.0.2.0/24", APIToken: strings.Repeat("a", 32), ProxyToken: strings.Repeat("b", 32), MaxBodyBytes: 1024, MaxFrameBytes: 32, MaxFrames: 3, MaxRecords: 20, MaxDiskBytes: 1 << 20, RetentionHours: 24, MaxConnections: 16, UpstreamCAFile: caFile}
	a, err := New(c, "fixture-fingerprint", gate)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProxy(a, ca)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p)
	a.Config.ProxyListen = srv.Listener.Addr().String()
	if err = a.UpdateDevice(context.Background(), selectedMAC, "Selected"); err != nil {
		t.Fatal(err)
	}
	if err = a.UpdateDevice(context.Background(), otherMAC, "Other"); err != nil {
		t.Fatal(err)
	}
	if err = a.reconcile(context.Background(), map[string][]string{selectedMAC: {"192.0.2.10"}, otherMAC: {"192.0.2.11"}}); err != nil {
		t.Fatal(err)
	}
	if err = a.Trust(context.Background(), selectedMAC, true, "fixture installed CA"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close(); srv.Close(); a.gate.Close() })
	return &fixture{gate, a, p, srv, ca, tls.Certificate{Certificate: [][]byte{leafDER, der}, PrivateKey: leafKey}, roots, dir}
}

// client reaches origins the way a gateway proxy node does: CONNECT into the
// service on one lane, naming the policy the gateway chose.
func (f *fixture) client(t *testing.T, lane string) *http.Client {
	t.Helper()
	u, _ := url.Parse(f.server.URL)
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: f.roots}, ForceAttemptHTTP2: true}
	// HTTP origins use CONNECT too, matching the gateway's universal TCP adapter.
	tr.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", u.Host)
		if err != nil {
			return nil, err
		}
		req := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{gateway.LaneHeader: {lane}, gateway.PolicyHeader: {"direct"}, "Proxy-Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(gateway.ProxyUser+":"+f.a.Config.ProxyToken))}}}
		if err = req.Write(c); err != nil {
			c.Close()
			return nil, err
		}
		br := bufio.NewReader(c)
		res, err := http.ReadResponse(br, req)
		if err != nil {
			c.Close()
			return nil, err
		}
		if res.StatusCode != 200 {
			c.Close()
			return nil, fmt.Errorf("CONNECT: %s", res.Status)
		}
		return &bufferConn{c, br}, nil
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func (f *fixture) origin(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(h)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{f.leaf}}
	s.EnableHTTP2 = true
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func records(t *testing.T, a *App, complete bool) []Record {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		items, _, err := a.store.List(Filter{Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		out := []Record{}
		for _, i := range items {
			r, err := a.store.Get(i.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !complete || r.Complete {
				out = append(out, *r)
			}
		}
		if len(out) > 0 || !complete {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatal("no completed record")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// contract: README.md — HTTP details, binary bodies, curl replay,
// filtering, and the same behavior on a nonstandard HTTPS port.
func TestHTTPSCaptureAndReplay(t *testing.T) {
	f := newFixture(t)
	want := []byte{0, 255, '\'', '$', '(', ')', '\n'}
	origin := f.origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.Header().Set("X-Seen-Method", r.Method)
		w.WriteHeader(201)
		_, _ = w.Write(b)
	}))
	if _, err := f.a.StartCapture(context.Background(), selectedMAC, nil); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", origin.URL+"/binary?q=quoted%27", bytes.NewReader(want))
	req.Header.Set("X-Probe", "literal $(printf bad) ' value")
	res, err := f.client(t, gateway.LaneCapture).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.ProtoMajor != 2 {
		t.Fatal("HTTPS capture did not negotiate HTTP/2")
	}
	if !bytes.Equal(got, want) || res.StatusCode != 201 {
		t.Fatalf("upstream changed: %d %q", res.StatusCode, got)
	}
	r := records(t, f.a, true)[0]
	if r.Method != "POST" || r.Status != 201 || r.URL != req.URL.String() || len(r.Response.Headers.Values("Set-Cookie")) != 2 || r.Request.Body.Encoding != "base64" || r.Response.Body.Encoding != "base64" || !r.Replayable {
		t.Fatalf("invalid captured details: %+v", r)
	}
	b, _ := base64.StdEncoding.DecodeString(r.Response.Body.Data)
	if !bytes.Equal(b, want) {
		t.Fatal("binary capture differs")
	}
	items, _, err := f.a.store.List(Filter{Host: "127.0.0.1", Path: "/binary", Method: "POST", Status: 201, Keyword: "literal"})
	if err != nil || len(items) != 1 {
		t.Fatalf("filter: %v %d", err, len(items))
	}
	api := httptest.NewServer(f.a.Routes(f.ca.Leaf, http.NotFoundHandler()))
	defer api.Close()
	listRequest, _ := http.NewRequest("GET", api.URL+"/v1/records", nil)
	listRequest.Header.Set("Authorization", "Bearer "+f.a.Config.APIToken)
	listResponse, err := http.DefaultClient.Do(listRequest)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Items []map[string]any `json:"items"`
	}
	err = json.NewDecoder(listResponse.Body).Decode(&wire)
	listResponse.Body.Close()
	if err != nil || listResponse.StatusCode != 200 || len(wire.Items) != 1 {
		t.Fatalf("record list: %d %v", listResponse.StatusCode, err)
	}
	if _, present := wire.Items[0]["request"]; present {
		t.Fatal("summary falsely represents omitted content as an empty request")
	}
	cmd := exec.Command("/bin/sh", "-c", r.Curl)
	cmd.Env = append(os.Environ(), "CURL_CA_BUNDLE="+f.a.Config.UpstreamCAFile, "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=")
	b, err = cmd.Output()
	if err != nil || !bytes.Equal(b, want) {
		t.Fatalf("curl replay: %v %q", err, b)
	}
}

// invariant: I1 — only the trusted, selected device is published to the
// capture lane; a capture-lane connection keeps the ticket it arrived with, so
// switching the capture and clearing records invalidate its later writes.
func TestCaptureIsolationAndLateWrites(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	origin := f.origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "unchanged") }))
	get := func(c *http.Client) *http.Response {
		t.Helper()
		res, err := c.Get(origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if string(b) != "unchanged" {
			t.Fatal("response changed")
		}
		return res
	}
	if _, err := f.a.StartCapture(ctx, otherMAC, nil); err == nil {
		t.Fatal("untrusted capture accepted")
	}
	if _, err := f.a.StartCapture(ctx, selectedMAC, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.a.Trust(ctx, otherMAC, true, "fixture installed CA"); err != nil {
		t.Fatal(err)
	}
	if sel := f.gate.published(); strings.Join(sel.CaptureIPs, ",") != "192.0.2.10" || strings.Join(sel.DailyIPs, ",") != "192.0.2.11" {
		t.Fatalf("published selection %+v", sel)
	}
	if _, err := f.a.StartCapture(ctx, otherMAC, nil); err == nil {
		t.Fatal("second device replaced an active capture")
	}
	// Daily-lane hosts outside every module pass through untouched.
	if res := get(f.client(t, gateway.LaneDaily)); !bytes.Equal(res.TLS.PeerCertificates[0].Raw, f.leaf.Certificate[0]) {
		t.Fatal("daily lane intercepted a host outside the module hostnames")
	}
	if got := records(t, f.a, false); len(got) != 0 {
		t.Fatalf("daily lane records: %d", len(got))
	}

	kept := f.client(t, gateway.LaneCapture)
	get(kept)
	if len(records(t, f.a, true)) != 1 {
		t.Fatal("capture lane not recorded")
	}
	if err := f.a.StopCapture(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.StartCapture(ctx, selectedMAC, nil); err != nil {
		t.Fatal(err)
	}
	get(kept)
	if n := len(records(t, f.a, false)); n != 1 {
		t.Fatalf("a connection from the previous capture wrote into the new one: %d records", n)
	}
	get(f.client(t, gateway.LaneCapture))
	if n := len(records(t, f.a, false)); n != 2 {
		t.Fatalf("new connection not recorded: %d records", n)
	}

	ticket := f.a.captureTicket()
	if err := f.a.ClearRecords(); err != nil {
		t.Fatal(err)
	}
	f.a.record(ticket, &Record{ID: newID(), At: time.Now(), URL: origin.URL})
	if len(records(t, f.a, false)) != 0 {
		t.Fatal("clear resurrected in-flight record")
	}

	// When clearing the previous capture was not confirmed, the gateway must
	// confirm an empty capture lane before another capture begins.
	f.gate.mu.Lock()
	f.gate.fail = errors.New("controller unavailable")
	f.gate.mu.Unlock()
	if err := f.a.StopCapture(ctx); err == nil {
		t.Fatal("unconfirmed stop reported success")
	}
	f.gate.mu.Lock()
	f.gate.fail = nil
	f.gate.history = nil
	f.gate.mu.Unlock()
	if _, err := f.a.StartCapture(ctx, otherMAC, nil); err != nil {
		t.Fatal(err)
	}
	f.gate.mu.Lock()
	history := f.gate.history
	f.gate.mu.Unlock()
	if len(history) != 2 || len(history[0].CaptureIPs) != 0 || strings.Join(history[1].CaptureIPs, ",") != "192.0.2.11" {
		t.Fatalf("selections before the new capture: %+v", history)
	}
}

// contract: README.md — HTTP is captured on nonstandard ports;
// non-HTTP TCP retains server-first banners and half-close response semantics.
func TestPlainHTTPAndServerFirstTCP(t *testing.T) {
	f := newFixture(t)
	if _, err := f.a.StartCapture(context.Background(), selectedMAC, nil); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, _ = gz.Write([]byte("searchable compressed response"))
	_ = gz.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	defer origin.Close()
	res, err := f.client(t, gateway.LaneCapture).Get(origin.URL + "/plain")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != "searchable compressed response" {
		t.Fatal("HTTP transport changed")
	}
	r := records(t, f.a, true)[0]
	if !strings.HasPrefix(r.URL, "http://") || r.Response.Body.Decoded == nil || r.Response.Body.Decoded.Data != "searchable compressed response" {
		t.Fatalf("HTTP capture: %+v", r)
	}
	if err = f.a.ClearRecords(); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.WriteString(c, "SSH-fixture\r\n")
		b, _ := io.ReadAll(c)
		_, _ = c.Write(append([]byte("after EOF:"), b...))
	}()
	tr := f.client(t, gateway.LaneCapture).Transport.(*http.Transport)
	conn, err := tr.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	b = make([]byte, len("SSH-fixture\r\n"))
	if _, err = io.ReadFull(conn, b); err != nil || string(b) != "SSH-fixture\r\n" {
		t.Fatalf("banner: %v %q", err, b)
	}
	_, _ = io.WriteString(conn, "payload")
	if err = conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	b, err = io.ReadAll(conn)
	if err != nil || string(b) != "after EOF:payload" {
		t.Fatalf("half close: %v %q", err, b)
	}
	if len(records(t, f.a, false)) != 0 {
		t.Fatal("non-HTTP traffic recorded")
	}
}

// contract: README.md — non-HTTP TLS with an explicit protocol
// stays an opaque tunnel, preserving the origin certificate.
func TestNonHTTPTLSALPNPassthrough(t *testing.T) {
	f := newFixture(t)
	if _, err := f.a.StartCapture(context.Background(), selectedMAC, nil); err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{f.leaf}, NextProtos: []string{"mqtt"}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.WriteString(c, "mqtt-ready")
	}()
	tr := f.client(t, gateway.LaneCapture).Transport.(*http.Transport)
	raw, err := tr.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
	c := tls.Client(raw, &tls.Config{RootCAs: f.roots, ServerName: "127.0.0.1", NextProtos: []string{"mqtt"}})
	b, err := io.ReadAll(c)
	if err != nil || string(b) != "mqtt-ready" {
		t.Fatalf("TLS tunnel: %v %q", err, b)
	}
	if !bytes.Equal(c.ConnectionState().PeerCertificates[0].Raw, f.leaf.Certificate[0]) {
		t.Fatal("non-HTTP TLS was intercepted")
	}
	if len(records(t, f.a, false)) != 0 {
		t.Fatal("non-HTTP TLS recorded")
	}
}

// invariant: I2 — capture never resumes after process restart; CA rotation
// invalidates stored trust attestations.
func TestRestartAndCARotation(t *testing.T) {
	f := newFixture(t)
	if _, err := f.a.StartCapture(context.Background(), selectedMAC, nil); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(f.a.Config, "fixture-fingerprint", nil)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.capture != nil || !restarted.devices[selectedMAC].Trusted {
		t.Fatal("restart state invalid")
	}
	rotated, err := New(f.a.Config, "another-root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.devices[selectedMAC].Trusted {
		t.Fatal("trust survived CA rotation")
	}
}

// contract: README.md — WebSocket records retain wire frames,
// including masked client payloads and a visible capture limit.
func TestWebSocketFrames(t *testing.T) {
	f := newFixture(t)
	origin := f.origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			kind, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err = c.Write(r.Context(), kind, b); err != nil {
				return
			}
		}
	}))
	if _, err := f.a.StartCapture(context.Background(), selectedMAC, nil); err != nil {
		t.Fatal(err)
	}
	u := "wss" + strings.TrimPrefix(origin.URL, "https") + "/socket"
	c, _, err := websocket.Dial(context.Background(), u, &websocket.DialOptions{HTTPClient: f.client(t, gateway.LaneCapture)})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		payload := []byte("hello websocket")
		if err = c.Write(context.Background(), websocket.MessageText, payload); err != nil {
			t.Fatal(err)
		}
		_, b, err := c.Read(context.Background())
		if err != nil || !bytes.Equal(b, payload) {
			t.Fatalf("websocket echo: %v %q", err, b)
		}
	}
	c.CloseNow()
	r := records(t, f.a, true)[0]
	if r.Status != 101 || len(r.Frames) != 3 || !r.FramesTruncated {
		t.Fatalf("frame capture: %d frames=%d truncated=%v", r.Status, len(r.Frames), r.FramesTruncated)
	}
	if r.Frames[0].Body.Data != "hello websocket" || r.Frames[0].Direction != "client_to_server" {
		t.Fatalf("masked frame differs: %+v", r.Frames[0])
	}
}

// contract: README.md — storage limits do not truncate forwarded
// traffic; an incomplete body must not claim to be directly replayable.
func TestLargeBodyPassthrough(t *testing.T) {
	f := newFixture(t)
	want := strings.Repeat("x", 8192)
	origin := f.origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(w, r.Body) }))
	if _, err := f.a.StartCapture(context.Background(), selectedMAC, nil); err != nil {
		t.Fatal(err)
	}
	res, err := f.client(t, gateway.LaneCapture).Post(origin.URL, "text/plain", strings.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != want {
		t.Fatal("large body changed")
	}
	r := records(t, f.a, true)[0]
	if !r.Request.Body.Truncated || !r.Response.Body.Truncated || r.Replayable || r.Curl != "" || r.Request.Body.Size != 8192 {
		t.Fatalf("truncation contract: %+v", r)
	}
}

func TestAPIAuthenticationAndModuleHostnamePatch(t *testing.T) {
	f := newFixture(t)
	dashboard := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	api := httptest.NewServer(f.a.Routes(f.ca.Leaf, dashboard))
	defer api.Close()
	for _, path := range []string{"/v1/status", "/v1/", "/v1/unknown"} {
		res, err := http.Get(api.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("API allowed unauthenticated access to %s: %d", path, res.StatusCode)
		}
	}
	// The dashboard's files are public; it reads data through the API with a token.
	res, err := http.Get(api.URL + "/devices")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusTeapot {
		t.Fatalf("dashboard route: %d", res.StatusCode)
	}
	// repro: a phone fetching the CA cannot send a token; the certificate returned 401.
	res, err = http.Get(api.URL + "/ca.pem")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if block, _ := pem.Decode(b); res.StatusCode != 200 || block == nil || !bytes.Equal(block.Bytes, f.ca.Leaf.Raw) {
		t.Fatalf("public CA download: %d", res.StatusCode)
	}
	// upstream: iOS and Android install a downloaded certificate when it is
	// served as application/x-x509-ca-cert.
	lan := httptest.NewServer(CARoutes(f.ca.Leaf.Raw))
	defer lan.Close()
	res, err = http.Get(lan.URL + "/ca.crt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/x-x509-ca-cert" || !bytes.Equal(b, f.ca.Leaf.Raw) {
		t.Fatalf("LAN CA download: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	// The LAN listener serves the certificate only: neither the API nor the dashboard.
	for _, path := range []string{"/v1/status", "/"} {
		if res, err = http.Get(lan.URL + path); err != nil || res.StatusCode != 404 {
			t.Fatalf("LAN CA listener exposed %s", path)
		}
		res.Body.Close()
	}
	path := filepath.Join(f.dir, "fixture.sgmodule")
	text := "#!name=Fixture\n#!arguments=host:example.com\n[Script]\nx = type=http-response,script-path=not-fetched.js\n[MITM]\nhostname = %APPEND% -blocked.{{{host}}}, *.{{{host}}}, 127.0.0.1\n"
	if err = os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := f.a.RegisterModule(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Processing != "hostname-only" {
		t.Fatalf("module %+v", m)
	}
	// Only trusted devices take the daily lane, limited to the module patch.
	if sel := f.gate.published(); strings.Join(sel.Hostnames, ",") != "-blocked.example.com,*.example.com,127.0.0.1" || strings.Join(sel.DailyIPs, ",") != "192.0.2.10" {
		t.Fatalf("published selection %+v", sel)
	}
	origin := f.origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	intercepted := func() bool {
		res, err := f.client(t, gateway.LaneDaily).Get(origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return !bytes.Equal(res.TLS.PeerCertificates[0].Raw, f.leaf.Certificate[0])
	}
	if !intercepted() || len(records(t, f.a, false)) != 0 {
		t.Fatal("daily lane must decrypt module hostnames without recording")
	}
	if err = f.a.RemoveModule(context.Background(), m.ID); err != nil {
		t.Fatal(err)
	}
	if sel := f.gate.published(); len(sel.Hostnames) != 0 || intercepted() {
		t.Fatal("removed module still selected")
	}
}

// contract: GET /v1/records, read by the dashboard — summaries name the device
// and the response's media type and size, are rebuilt from the record files
// after a restart, and the device parameter takes any MAC notation.
func TestRecordListByDevice(t *testing.T) {
	f := newFixture(t)
	body := `{"ok":true}`
	for _, device := range []string{selectedMAC, otherMAC} {
		r := &Record{ID: newID(), DeviceID: device, Session: 1, At: time.Now().UTC(), Method: "GET", URL: "https://api.example.com/feed", Host: "api.example.com", Path: "/feed", Status: 200, Complete: true,
			Response: Message{Headers: http.Header{"Content-Type": {"Application/JSON; charset=utf-8"}}, Body: Body{Encoding: "utf8", Data: body, Size: int64(len(body))}}}
		if err := f.a.store.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := New(f.a.Config, f.a.Fingerprint, f.gate)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(restarted.Routes(f.ca.Leaf, http.NotFoundHandler()))
	defer api.Close()
	list := func(query string) (int, []map[string]any) {
		req, _ := http.NewRequest("GET", api.URL+"/v1/records?"+query, nil)
		req.Header.Set("Authorization", "Bearer "+f.a.Config.APIToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var wire struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.NewDecoder(res.Body).Decode(&wire)
		return res.StatusCode, wire.Items
	}
	status, items := list("device=00-11-22-33-44-55")
	if status != 200 || len(items) != 1 {
		t.Fatalf("device filter: %d %d items", status, len(items))
	}
	if items[0]["device_id"] != selectedMAC || items[0]["content_type"] != "application/json" || items[0]["response_size"] != float64(len(body)) {
		t.Fatalf("summary after restart: %v", items[0])
	}
	if status, _ = list("device=phone"); status != 400 {
		t.Fatalf("invalid device: %d", status)
	}
}

// contract: README.md — TTL, byte and count retention bound on-disk
// capture files, including across a storage reopen.
func TestRetentionBounds(t *testing.T) {
	f := newFixture(t)
	s := f.a.store
	s.c.MaxRecords = 2
	s.c.MaxDiskBytes = 20000
	for i := 0; i < 4; i++ {
		r := &Record{ID: newID(), At: time.Now().Add(time.Duration(i) * time.Millisecond), Response: Message{Body: Body{Encoding: "utf8", Data: strings.Repeat("x", 1000)}}}
		if err := s.Put(r); err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := s.List(Filter{})
	if err != nil || len(items) != 2 {
		t.Fatalf("count retention %d %v", len(items), err)
	}
	entries, _ := os.ReadDir(s.dir)
	var size int64
	for _, e := range entries {
		info, _ := e.Info()
		size += info.Size()
	}
	if size > s.c.MaxDiskBytes {
		t.Fatal("disk budget exceeded")
	}
	old := &Record{ID: newID(), At: time.Now().Add(-25 * time.Hour)}
	if err = s.Put(old); err != nil {
		t.Fatal(err)
	}
	if err = s.Prune(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(old.ID); !os.IsNotExist(err) {
		t.Fatal("expired record remains")
	}
	reopened, err := NewStore(s.dir, s.c)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.bytes > s.c.MaxDiskBytes {
		t.Fatal("reopened storage over budget")
	}
}
