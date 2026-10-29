package app

import (
	"context"
	"testing"
)

// repro: macOS arp -an on 2026-10-29 — actual neighbors include
// single-digit MAC octets, incomplete entries and a broadcast address.
func TestMacOSARPDeviceBindings(t *testing.T) {
	text := `? (192.168.31.79) at 6:32:1e:ff:86:65 on en0 ifscope [ethernet]
? (192.168.31.168) at 9c:c7:d3:5b:2:8 on en0 ifscope [ethernet]
? (192.168.31.174) at (incomplete) on en0 ifscope [ethernet]
? (192.168.31.255) at ff:ff:ff:ff:ff:ff on en0 ifscope [ethernet]
? (192.168.97.0) at d2:11:e5:20:d:66 on bridge102 ifscope permanent [bridge]
`
	bindings := parseARP(text, "192.168.31.0/24")
	if len(bindings) != 2 {
		t.Fatalf("bindings = %#v", bindings)
	}
	if got := bindings["06:32:1e:ff:86:65"]; len(got) != 1 || got[0] != "192.168.31.79" {
		t.Fatalf("single-digit MAC: %#v", got)
	}
	if got := bindings["9c:c7:d3:5b:02:08"]; len(got) != 1 || got[0] != "192.168.31.168" {
		t.Fatalf("device IP binding: %#v", got)
	}
}

// repro: a gateway lists itself (192.168.31.2, its own en0 MAC) in arp -an, and
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
	if err := f.a.reconcile(context.Background(), map[string][]string{self: {"192.168.31.2"}, selectedMAC: {"192.168.31.10"}}); err != nil {
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
