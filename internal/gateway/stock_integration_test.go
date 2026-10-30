package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Two sources stand in for LAN devices: ::1 is the selected device and
// 127.0.0.1 is everyone else, which is also where the capture service
// re-enters the gateway from. The rules depend on source metadata: they depend on the
// source address and source port, and exempt the gateway's own loopback.
const stockConfig = `mixed-port: %d
allow-lan: true
bind-address: "*"
mode: rule
log-level: info
external-controller-unix: %s
ipv6: true
dns:
  enable: false
hosts:
  original.invalid: 127.0.0.1
  routed.invalid: 127.0.0.1
  blocked.invalid: 127.0.0.1
  plain.invalid: 127.0.0.1
proxies:
  - name: Alt
    type: http
    server: %s
    port: %s
sub-rules:
  denied:
    - DOMAIN,blocked.invalid,REJECT
rules:
  - SUB-RULE,(DOMAIN,blocked.invalid),denied
  - AND,((SRC-IP-CIDR,::1/128),(SRC-PORT,%d),(DOMAIN,original.invalid)),DIRECT
  - SRC-IP-CIDR,127.0.0.1/32,DIRECT
  - DOMAIN,routed.invalid,Alt
  - DOMAIN,plain.invalid,DIRECT
  - NETWORK,UDP,DIRECT
  - MATCH,REJECT
`

type stockCore struct {
	t      *testing.T
	client *Client
	mixed  int
	logs   *bytes.Buffer
}

// invariant: I3 — a captured connection takes the policy the original rules
// choose from the device's own source address and port.
// contract: README.md — unselected devices and hosts outside the
// hostname set keep their path; the gateway config file is never written; the
// lanes return after the gateway reloads its own config.
func TestStockCoreLanes(t *testing.T) {
	core, origin, sourcePort, alternateHits := startStockCore(t)
	c := core.client
	ctx := context.Background()
	original, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lanes []string
	capture := connectProxy(t, func(req *http.Request) (net.Conn, error) {
		if req.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(ProxyUser+":"+c.Token)) {
			return nil, fmt.Errorf("proxy node did not authenticate")
		}
		mu.Lock()
		lanes = append(lanes, req.Header.Get(LaneHeader))
		mu.Unlock()
		return c.DialContext(req.Context(), req.Header.Get(PolicyHeader), req.Host)
	})
	captureURL, _ := url.Parse(capture.URL)
	c.ProxyAddress = captureURL.Host
	seen := func(want ...string) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if strings.Join(lanes, ",") != strings.Join(want, ",") {
			t.Fatalf("lanes %v, want %v", lanes, want)
		}
	}
	selected := func(port int, host string) func() (net.Conn, error) {
		return func() (net.Conn, error) { return viaCore("[::1]:"+strconv.Itoa(core.mixed), port, host+":"+origin) }
	}
	other := func(host string) func() (net.Conn, error) {
		return func() (net.Conn, error) { return viaCore("127.0.0.1:"+strconv.Itoa(core.mixed), 0, host+":"+origin) }
	}

	if err = c.Select(ctx, Selection{CaptureIPs: []string{"::1"}}); err != nil {
		t.Fatal("select: ", err, core.logs.String())
	}
	assertEcho(t, selected(sourcePort, "original.invalid"), true)
	seen("capture")
	// From any other source port the original rules reject this host; leaving
	// from loopback must not turn that into the loopback exemption.
	assertEcho(t, selected(0, "original.invalid"), false)
	assertEcho(t, selected(0, "blocked.invalid"), false)
	assertEcho(t, selected(0, "routed.invalid"), true)
	if alternateHits.Load() != 1 {
		t.Fatalf("captured connection left through %d Alt connections, want 1", alternateHits.Load())
	}
	assertEcho(t, other("original.invalid"), true)
	seen("capture", "capture")
	// Server-first bytes and half-close cross both gateway hops of the lane.
	banner := bannerAfterEOF(t)
	for attempt := 0; attempt < 8; attempt++ {
		conn, err := viaCore("[::1]:"+strconv.Itoa(core.mixed), 0, "plain.invalid:"+banner)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		b := make([]byte, len("server-first\n"))
		if _, err = io.ReadFull(conn, b); err != nil || string(b) != "server-first\n" {
			t.Fatalf("server-first bytes: %v %q", err, b)
		}
		io.WriteString(conn, "payload")
		if err = conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
			t.Fatal(err)
		}
		b, err = io.ReadAll(conn)
		conn.Close()
		if err != nil || string(b) != "after-eof:payload" {
			t.Fatalf("half-close: %v %q", err, b)
		}
	}
	mu.Lock()
	lanes = lanes[:2]
	mu.Unlock()

	// A module's hostname list decides by its first matching entry, so the
	// exclusion before the wildcard keeps plain.invalid out of the lane.
	daily := Selection{Daily: []DailyLane{{IPs: []string{"::1"}, Hostnames: [][]string{{"-plain.invalid", "*.invalid"}}}}}
	if err = c.Select(ctx, daily); err != nil {
		t.Fatal(err)
	}
	assertEcho(t, selected(0, "routed.invalid"), true)
	assertEcho(t, selected(0, "plain.invalid"), true)
	seen("capture", "capture", "daily-0")

	// The gateway reloading its own file stands in for its restart.
	if err = c.call(ctx, "PUT", "/configs", map[string]string{}, nil); err != nil {
		t.Fatal(err)
	}
	if status, err := c.Status(ctx); err != nil || status.Catalog {
		t.Fatalf("reloaded gateway still has lanes: %+v %v", status, err)
	}
	assertEcho(t, selected(0, "routed.invalid"), true)
	seen("capture", "capture", "daily-0")
	if err = c.Select(ctx, daily); err != nil {
		t.Fatal(err)
	}
	assertEcho(t, selected(0, "routed.invalid"), true)
	seen("capture", "capture", "daily-0", "daily-0")

	// The LaunchAgent wrapper runs Clear after the service process ends.
	if err = c.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	assertEcho(t, selected(0, "routed.invalid"), true)
	seen("capture", "capture", "daily-0", "daily-0")
	if after, _ := os.ReadFile(c.ConfigPath); !bytes.Equal(after, original) {
		t.Fatal("gateway config file was written")
	}
	if err = c.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err := c.Status(ctx); err != nil || status.Catalog {
		t.Fatalf("removed lanes remain: %+v %v", status, err)
	}
}

// contract: docs/mihomo.md — devices with different module sets take
// different daily lanes, each limited to the hostnames of its own modules; a
// selection with more sets than the catalog holds reloads it with more lanes.
func TestStockCoreDailyLanes(t *testing.T) {
	core, origin, _, _ := startStockCore(t)
	c := core.client
	ctx := context.Background()
	var mu sync.Mutex
	var seen []string
	proxy := connectProxy(t, func(req *http.Request) (net.Conn, error) {
		mu.Lock()
		seen = append(seen, req.Header.Get(LaneHeader))
		mu.Unlock()
		return c.DialContext(req.Context(), req.Header.Get(PolicyHeader), req.Host)
	})
	u, _ := url.Parse(proxy.URL)
	c.ProxyAddress = u.Host
	from := func(source, host string) func() (net.Conn, error) {
		return func() (net.Conn, error) { return viaCore(source+":"+strconv.Itoa(core.mixed), 0, host+":"+origin) }
	}
	want := func(lanes ...string) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if strings.Join(seen, ",") != strings.Join(lanes, ",") {
			t.Fatalf("lanes %v, want %v", seen, lanes)
		}
		seen = nil
	}
	sel := Selection{Daily: []DailyLane{
		{IPs: []string{"::1"}, Hostnames: [][]string{{"routed.invalid"}}},
		{IPs: []string{"127.0.0.1"}, Hostnames: [][]string{{"plain.invalid"}, {"original.invalid"}}},
	}}
	if err := c.Select(ctx, sel); err != nil {
		t.Fatal(err)
	}
	assertEcho(t, from("[::1]", "routed.invalid"), true)
	assertEcho(t, from("[::1]", "plain.invalid"), true)
	assertEcho(t, from("127.0.0.1", "plain.invalid"), true)
	assertEcho(t, from("127.0.0.1", "original.invalid"), true)
	assertEcho(t, from("127.0.0.1", "routed.invalid"), true)
	// ::1's plain.invalid and 127.0.0.1's routed.invalid stay outside.
	want("daily-0", "daily-1", "daily-1")

	sel.Daily = append(sel.Daily, DailyLane{IPs: []string{"192.0.2.1"}, Hostnames: [][]string{{"*"}}})
	if err := c.Select(ctx, sel); err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(ctx)
	if _, grown := status.Rules["daily-3"]; err != nil || !grown || status.Rules["daily-1"] != 1 {
		t.Fatalf("catalog after growth: %+v %v", status, err)
	}
	assertEcho(t, from("127.0.0.1", "plain.invalid"), true)
	want("daily-1")
	if err = c.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err = c.Status(ctx); err != nil || status.Rules["daily-1"] != 0 || status.Rules["daily-3"] != 0 {
		t.Fatalf("lanes after clear: %+v %v", status, err)
	}
	assertEcho(t, from("127.0.0.1", "plain.invalid"), true)
	want()
}

// contract: README.md — HTTP/3 falls back to TCP only when the
// selected device's QUIC is dropped; other UDP and other devices keep going.
func TestStockCoreBlocksSelectedUDPPorts(t *testing.T) {
	core, _, _, _ := startStockCore(t)
	blocked, open := udpEcho(t), udpEcho(t)
	if err := core.client.Select(context.Background(), Selection{CaptureIPs: []string{"::1"}, BlockUDPPorts: []uint16{uint16(blocked)}}); err != nil {
		t.Fatal(err)
	}
	if udpViaCore(t, "::1", core.mixed, blocked) {
		t.Fatal("blocked UDP port answered for the selected device")
	}
	if !udpViaCore(t, "::1", core.mixed, open) {
		t.Fatal("unblocked UDP port failed for the selected device")
	}
	if !udpViaCore(t, "127.0.0.1", core.mixed, blocked) {
		t.Fatal("UDP block reached another device")
	}
}

// contract: docs/mihomo.md — configurations the lanes cannot
// reproduce are refused rather than captured with another outcome.
func TestCatalogRefusesUnreproducibleRouting(t *testing.T) {
	o := catalogOptions{ProxyAddress: "127.0.0.1:19091", EgressAddress: "127.0.0.1:19092", Token: strings.Repeat("a", 32), StateDir: t.TempDir()}
	if _, err := compile([]byte("mode: rule\nrules:\n  - MATCH,DIRECT\n"), o); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]string{
		"source-hashed group": "proxy-groups:\n  - {name: lb, type: load-balance, proxies: [DIRECT]}\n  - {name: outer, type: select, proxies: [lb]}\nrules:\n  - MATCH,outer\n",
		"PASS":                "rules:\n  - DOMAIN,a.invalid,PASS\n  - MATCH,DIRECT\n",
		"global mode":         "mode: global\nrules:\n  - MATCH,DIRECT\n",
		// A url-test group with include-all-proxies lists the lane nodes as
		// members (stock core v1.19.33) and could route normal traffic into them.
		"include-all group": "proxy-groups:\n  - {name: auto, type: url-test, include-all-proxies: true}\nrules:\n  - MATCH,auto\n",
	} {
		if _, err := compile([]byte(config), o); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func startStockCore(t *testing.T) (*stockCore, string, int, *atomic.Int32) {
	t.Helper()
	binaryPath := os.Getenv("MIHOMO_TEST_BINARY")
	if binaryPath == "" {
		t.Skip("set MIHOMO_TEST_BINARY to an unmodified mihomo binary")
	}
	// UNIX socket paths are short; t.TempDir is often too long on macOS.
	dir, err := os.MkdirTemp("/tmp", "oc-stock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	origin := echoServer(t)
	_, originPort, _ := net.SplitHostPort(origin)
	alternateHits := &atomic.Int32{}
	alternate := connectProxy(t, func(req *http.Request) (net.Conn, error) {
		alternateHits.Add(1)
		_, port, _ := net.SplitHostPort(req.Host)
		return net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
	})
	alternateURL, _ := url.Parse(alternate.URL)
	alternateHost, alternatePort, _ := net.SplitHostPort(alternateURL.Host)
	mixed, sourcePort := freePort(t), freePort6(t)
	socket := filepath.Join(dir, "controller.sock")
	configPath := filepath.Join(dir, "config.yaml")
	config := fmt.Sprintf(stockConfig, mixed, socket, alternateHost, alternatePort, sourcePort)
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	cmd := exec.Command(binaryPath, "-d", dir, "-f", configPath)
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Log(logs.String())
		}
	})
	c := NewClient(socket, configPath, filepath.Join(dir, "mihomo-capture"))
	c.Token = strings.Repeat("t", 32)
	c.ProxyAddress = "127.0.0.1:1"
	c.EgressAddress = "127.0.0.1:" + strconv.Itoa(freePort(t))
	t.Cleanup(c.Close)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = c.Status(context.Background()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("core startup: ", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return &stockCore{t, c, mixed, logs}, originPort, sourcePort, alternateHits
}

func connectProxy(t *testing.T, dial func(*http.Request) (net.Conn, error)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			http.Error(w, "CONNECT", 405)
			return
		}
		up, err := dial(r)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer up.Close()
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		pipe(&bufferedConn{conn, rw.Reader}, up)
	}))
	t.Cleanup(server.Close)
	return server
}

func pipe(a, b net.Conn) {
	ch := make(chan struct{}, 2)
	copyHalf := func(dst, src net.Conn) {
		io.Copy(dst, src)
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			c.CloseWrite()
		}
		ch <- struct{}{}
	}
	go copyHalf(a, b)
	go copyHalf(b, a)
	<-ch
	<-ch
	a.Close()
	b.Close()
}

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()
	return ln.Addr().String()
}

func bannerAfterEOF(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.WriteString(c, "server-first\n")
				b, _ := io.ReadAll(c)
				c.Write(append([]byte("after-eof:"), b...))
			}()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func udpEcho(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		b := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(b)
			if err != nil {
				return
			}
			pc.WriteTo(b[:n], addr)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

// udpViaCore sends one datagram from source through the core's SOCKS5 UDP
// relay to 127.0.0.1:port and reports whether the echo came back.
func udpViaCore(t *testing.T, source string, mixed, port int) bool {
	t.Helper()
	relay := net.JoinHostPort(source, strconv.Itoa(mixed))
	control, err := net.Dial("tcp", relay)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	control.SetDeadline(time.Now().Add(3 * time.Second))
	reply := make([]byte, 2)
	control.Write([]byte{5, 1, 0})
	if _, err = io.ReadFull(control, reply); err != nil || reply[1] != 0 {
		t.Fatalf("SOCKS5 greeting: %v %v", err, reply)
	}
	control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	head := make([]byte, 4)
	if _, err = io.ReadFull(control, head); err != nil || head[1] != 0 {
		t.Fatalf("UDP ASSOCIATE: %v %v", err, head)
	}
	skip := map[byte]int{1: 4 + 2, 4: 16 + 2}[head[3]]
	if _, err = io.ReadFull(control, make([]byte, skip)); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", relay)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	packet := []byte{0, 0, 0, 1, 127, 0, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(packet[8:], uint16(port))
	payload := []byte("quic-initial")
	for attempt := 0; attempt < 3; attempt++ {
		conn.Write(append(packet, payload...))
		conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		b := make([]byte, 1500)
		n, err := conn.Read(b)
		if err == nil && n >= len(packet) && bytes.Equal(b[len(packet):n], payload) {
			return true
		}
	}
	return false
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func freePort6(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func viaCore(address string, sourcePort int, target string) (net.Conn, error) {
	host, _, _ := net.SplitHostPort(address)
	d := net.Dialer{Timeout: 3 * time.Second}
	if sourcePort > 0 {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(host), Port: sourcePort}
	}
	conn, err := d.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	req := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if err = req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != 200 {
		conn.Close()
		return nil, fmt.Errorf("core CONNECT: %s", resp.Status)
	}
	return &bufferedConn{conn, reader}, nil
}

func assertEcho(t *testing.T, dial func() (net.Conn, error), allow bool) {
	t.Helper()
	conn, err := dial()
	if err != nil {
		if allow {
			t.Fatal(err)
		}
		return
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	want := []byte("\x00\xffraw-echo\n")
	_, err = conn.Write(want)
	got := make([]byte, len(want))
	if err == nil {
		_, err = io.ReadFull(conn, got)
	}
	if allow && (err != nil || !bytes.Equal(got, want)) {
		t.Fatalf("echo %v %q", err, got)
	}
	if !allow && err == nil && bytes.Equal(got, want) {
		t.Fatal("a connection the original rules reject went through")
	}
}
