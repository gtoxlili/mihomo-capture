package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// contract: README.md — local capture needs no LAN discovery, keeps trust
// confirmation, and uses the same recording lifecycle as gateway devices.
func TestLocalCaptureLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.a.Config.Mode = "local"
	f.a.Config.LAN = ""
	f.a.Config.LocalSourceIPs = []string{"198.18.0.1"}
	if err := f.a.RefreshDevices(ctx); err != nil {
		t.Fatal(err)
	}
	f.a.mu.Lock()
	d := f.a.devices["local"]
	f.a.mu.Unlock()
	if !d.Online || !strings.Contains(strings.Join(d.IPs, ","), "198.18.0.1") {
		t.Fatalf("local device: %+v", d)
	}
	if _, err := f.a.StartCapture(ctx, "local", nil); err == nil {
		t.Fatal("untrusted local capture accepted")
	}
	if err := f.a.Trust(ctx, "local", true, "installed current CA"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.StartCapture(ctx, "local", nil); err != nil {
		t.Fatal(err)
	}
	ticket := f.a.captureTicket()
	f.a.record(ticket, &Record{ID: strings.Repeat("a", 32), At: time.Now(), Host: "example.test", Complete: true})
	req := httptest.NewRequest("GET", "/v1/records?device=local", nil)
	req.Header.Set("Authorization", "Bearer "+f.a.Config.APIToken)
	out := httptest.NewRecorder()
	f.a.Routes(f.ca.Leaf, http.NotFoundHandler()).ServeHTTP(out, req)
	if out.Code != 200 || !strings.Contains(out.Body.String(), strings.Repeat("a", 32)) {
		t.Fatalf("local filter: %d %s", out.Code, out.Body.String())
	}
	if err := f.a.StopCapture(ctx); err != nil {
		t.Fatal(err)
	}
	f.a.record(ticket, &Record{ID: strings.Repeat("b", 32), At: time.Now(), Complete: true})
	out = httptest.NewRecorder()
	f.a.Routes(f.ca.Leaf, http.NotFoundHandler()).ServeHTTP(out, req)
	if strings.Contains(out.Body.String(), strings.Repeat("b", 32)) {
		t.Fatal("stopped connection wrote another record")
	}
}

// upstream: ip-neighbour(8) output; only bound IPv4 hosts in the configured
// subnet can become gateway capture targets.
func TestLinuxNeighborBindings(t *testing.T) {
	got := parseNeighbors("192.168.1.20 dev eth0 lladdr 02:11:22:33:44:55 REACHABLE\n192.168.1.30 dev eth0 FAILED\n10.0.0.2 dev eth1 lladdr 02:11:22:33:44:66 STALE\n", "192.168.1.0/24")
	if len(got) != 1 || len(got["02:11:22:33:44:55"]) != 1 || got["02:11:22:33:44:55"][0] != "192.168.1.20" {
		t.Fatalf("neighbors: %#v", got)
	}
}
