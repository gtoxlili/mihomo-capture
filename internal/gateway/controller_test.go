package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
)

// contract: config.example.yaml — TCP controllers receive their configured
// secret as a Bearer token, never as part of the URL.
func TestHTTPControllerAuthentication(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-controller-secret" {
			t.Error("missing controller authentication")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/version":
			w.Write([]byte(`{"version":"fixture"}`))
		case "/providers/rules":
			w.Write([]byte(`{"providers":{}}`))
		default:
			t.Error("unexpected path: " + r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	c := NewClient("", "", "")
	defer c.Close()
	if err := c.UseHTTP(s.URL, "test-controller-secret"); err != nil {
		t.Fatal(err)
	}
	status, err := c.Status(context.Background())
	if err != nil || status.Version != "fixture" {
		t.Fatalf("controller status: %+v %v", status, err)
	}
}

// contract: README.md — loopback capture must exit through the dedicated
// listener without matching the capture lane a second time.
func TestStockCoreLocalCapture(t *testing.T) {
	core, origin, _, _ := startStockCore(t)
	c := core.client
	var hits atomic.Int32
	proxy := connectProxy(t, func(req *http.Request) (net.Conn, error) {
		if hits.Add(1) > 1 {
			return nil, fmt.Errorf("local capture recursed")
		}
		return c.DialContext(req.Context(), req.Header.Get(PolicyHeader), req.Host)
	})
	u, _ := url.Parse(proxy.URL)
	c.ProxyAddress = u.Host
	if err := c.Select(context.Background(), Selection{CaptureIPs: []string{"127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	assertEcho(t, func() (net.Conn, error) {
		return viaCore("127.0.0.1:"+strconv.Itoa(core.mixed), 0, "original.invalid:"+origin)
	}, true)
	if hits.Load() != 1 {
		t.Fatalf("capture hops: %d", hits.Load())
	}
	if err := c.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertEcho(t, func() (net.Conn, error) {
		return viaCore("127.0.0.1:"+strconv.Itoa(core.mixed), 0, "original.invalid:"+origin)
	}, true)
	if hits.Load() != 1 {
		t.Fatalf("capture continued after clear: %d", hits.Load())
	}
}
