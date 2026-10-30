package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BSD arp output can contain
// single-digit MAC octets, incomplete entries and a broadcast address.
func TestMacOSARPDeviceBindings(t *testing.T) {
	text := `? (192.0.2.79) at 2:0:0:0:0:1 on en0 ifscope [ethernet]
? (192.0.2.168) at 2:0:0:0:0:2 on en0 ifscope [ethernet]
? (192.0.2.174) at (incomplete) on en0 ifscope [ethernet]
? (192.0.2.255) at ff:ff:ff:ff:ff:ff on en0 ifscope [ethernet]
? (198.51.100.0) at 2:0:0:0:0:3 on bridge102 ifscope permanent [bridge]
`
	bindings := parseARP(text, "192.0.2.0/24")
	if len(bindings) != 2 {
		t.Fatalf("bindings = %#v", bindings)
	}
	if got := bindings["02:00:00:00:00:01"]; len(got) != 1 || got[0] != "192.0.2.79" {
		t.Fatalf("single-digit MAC: %#v", got)
	}
	if got := bindings["02:00:00:00:00:02"]; len(got) != 1 || got[0] != "192.0.2.168" {
		t.Fatalf("device IP binding: %#v", got)
	}
}

// repro: a gateway lists itself (192.0.2.2, its own en0 MAC) in arp -an, and
// the label map names it; its traffic never enters mihomo from that address.
func TestGatewayHostIsNotADevice(t *testing.T) {
	var self string
	for mac := range localMACs() {
		self = mac
		break
	}
	if self == "" {
		t.Skip("this machine has no interface with a MAC address")
	}
	f := newFixture(t)
	if err := f.a.UpdateDevice(context.Background(), self, "gateway"); err != nil {
		t.Fatal(err)
	}
	if err := f.a.reconcile(context.Background(), map[string][]string{self: {"192.0.2.2"}, selectedMAC: {"192.0.2.10"}}); err != nil {
		t.Fatal(err)
	}
	f.a.mu.Lock()
	_, listed := f.a.devices[self]
	f.a.mu.Unlock()
	if listed {
		t.Fatal("the gateway host is listed as a capture device")
	}
	if _, err := f.a.StartCapture(context.Background(), self, nil); err == nil {
		t.Fatal("capture of the gateway host accepted")
	}
}

// contract: docs/api.md — gateway device IDs are MAC addresses; `local` names
// a device only in local mode, so gateway mode never stores one.
func TestGatewayModeRejectsLocalDeviceID(t *testing.T) {
	f := newFixture(t)
	f.a.Config.Mode = "gateway"
	req := httptest.NewRequest("PUT", "/v1/devices/local", strings.NewReader(`{"label":"phantom"}`))
	req.Header.Set("Authorization", "Bearer "+f.a.Config.APIToken)
	out := httptest.NewRecorder()
	f.a.Routes(f.ca.Leaf, http.NotFoundHandler()).ServeHTTP(out, req)
	f.a.mu.Lock()
	_, stored := f.a.devices["local"]
	f.a.mu.Unlock()
	if out.Code < 400 || stored {
		t.Fatalf("rename local in gateway mode: %d %s, stored %v", out.Code, out.Body.String(), stored)
	}
}
