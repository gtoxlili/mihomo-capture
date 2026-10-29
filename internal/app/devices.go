package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

var arpDiscoveryLog sync.Once

var arpLine = regexp.MustCompile(`\(([^)]+)\) at ([0-9a-fA-F:]+) on ([^ ]+)`)

func parseARP(text, subnet string) map[string][]string {
	_, lan, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil
	}
	m := map[string][]string{}
	for _, line := range strings.Split(text, "\n") {
		p := arpLine.FindStringSubmatch(line)
		if len(p) == 0 {
			continue
		}
		ip := net.ParseIP(p[1])
		if !lan.Contains(ip) {
			continue
		}
		// macOS arp emits single digit octets, net.ParseMAC requires pairs.
		parts := strings.Split(p[2], ":")
		if len(parts) != 6 {
			continue
		}
		for i := range parts {
			if len(parts[i]) == 1 {
				parts[i] = "0" + parts[i]
			}
		}
		hardware, err := net.ParseMAC(strings.Join(parts, ":"))
		// ARP tables also include broadcast and multicast destinations.
		if err != nil || len(hardware) != 6 || hardware[0]&1 != 0 {
			continue
		}
		mac, err := normalizeMAC(hardware.String())
		if err != nil {
			continue
		}
		m[mac] = append(m[mac], ip.String())
	}
	for k := range m {
		sort.Strings(m[k])
		m[k] = unique(m[k])
	}
	return m
}

// localMACs lists this host's interfaces. The gateway shows up in its own
// neighbor table, but its traffic reaches mihomo from loopback and the TUN
// address, so it is never a capture source.
func localMACs() map[string]bool {
	out := map[string]bool{}
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		if mac, err := normalizeMAC(i.HardwareAddr.String()); err == nil {
			out[mac] = true
		}
	}
	return out
}

func unique(s []string) []string {
	if len(s) == 0 {
		return []string{}
	}
	r := []string{s[0]}
	for _, v := range s[1:] {
		if v != r[len(r)-1] {
			r = append(r, v)
		}
	}
	return r
}

func (a *App) RefreshDevices(ctx context.Context) error {
	if a.Config.Mode != "gateway" {
		ips := []string{"127.0.0.1", "::1"}
		interfaces, err := net.Interfaces()
		if err != nil {
			return err
		}
		for _, iface := range interfaces {
			if iface.Flags&net.FlagUp == 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				return err
			}
			for _, addr := range addrs {
				ip, _, err := net.ParseCIDR(addr.String())
				if err == nil && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified() {
					ips = append(ips, ip.String())
				}
			}
		}
		ips = append(ips, a.Config.LocalSourceIPs...)
		sort.Strings(ips)
		return a.reconcile(ctx, map[string][]string{localDeviceID: unique(ips)})
	}
	if runtime.GOOS == "linux" {
		b, err := exec.CommandContext(ctx, "ip", "-4", "neigh", "show").Output()
		if err != nil {
			return fmt.Errorf("neighbor discovery requires iproute2: %w", err)
		}
		return a.reconcile(ctx, parseNeighbors(string(b), a.Config.LAN))
	}
	if runtime.GOOS != "darwin" {
		return errors.New("gateway discovery supports macOS and Linux")
	}

	cmd := exec.CommandContext(ctx, "/usr/sbin/arp", "-an")
	if a.Config.ARPUseSudo {
		cmd = exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "/usr/sbin/arp", "-an")
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ARP discovery: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	neighbors := parseARP(string(b), a.Config.LAN)
	arpDiscoveryLog.Do(func() {
		args := []any{"stdout_bytes", len(b), "stderr_bytes", stderr.Len(), "neighbors", len(neighbors), "lan", a.Config.LAN}
		if stderr.Len() > 0 {
			args = append(args, "stderr", stderr.String())
		}
		slog.Info("ARP device discovery", args...)
	})
	return a.reconcile(ctx, neighbors)
}

func (a *App) reconcile(ctx context.Context, neighbors map[string][]string) error {
	labels := map[string]string{}
	if a.Config.DeviceMapFile != "" {
		b, err := os.ReadFile(a.Config.DeviceMapFile)
		if err != nil {
			return err
		}
		if err = yaml.Unmarshal(b, &labels); err != nil {
			return err
		}
		normalized := map[string]string{}
		for k, v := range labels {
			mac, err := normalizeMAC(k)
			if err != nil {
				return errors.New("invalid MAC in device map")
			}
			normalized[mac] = v
		}
		labels = normalized
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	for id := range a.self {
		delete(neighbors, id)
		delete(labels, id)
		if _, ok := a.devices[id]; ok {
			delete(a.devices, id)
			changed = true
		}
	}
	for id, d := range a.devices {
		ips, ok := neighbors[id]
		if d.Online != ok || strings.Join(d.IPs, ",") != strings.Join(ips, ",") {
			changed = true
		}
		d.Online = ok
		d.IPs = append([]string{}, ips...)
		a.devices[id] = d
	}
	for id, ips := range neighbors {
		d, ok := a.devices[id]
		if !ok {
			d = Device{ID: id, MAC: id, Label: labels[id], IPs: ips, Online: true}
			if id == localDeviceID {
				d.Label = "本机"
				d.MAC = ""
			}
			changed = true
		}
		a.devices[id] = d
	}
	for id, label := range labels {
		d, ok := a.devices[id]
		if !ok {
			d = Device{ID: id, MAC: id, IPs: []string{}}
			changed = true
		}
		if d.Label == "" {
			d.Label = label
			changed = true
		}
		a.devices[id] = d
	}
	if a.capture != nil && !a.devices[a.capture.DeviceID].Online {
		a.capture = nil
		a.generation++
		changed = true
	}
	if changed {
		if err := a.saveLocked(); err != nil {
			return err
		}
	}
	// Unchanged selections cost one controller read; this also reloads the
	// capture lanes after the gateway has reloaded its own config.
	return a.syncLocked(ctx)
}

const localDeviceID = "local"

// Linux iproute2 neighbors use a different format from BSD arp.
func parseNeighbors(text, subnet string) map[string][]string {
	_, lan, err := net.ParseCIDR(subnet)
	out := map[string][]string{}
	if err != nil {
		return out
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		ip := net.ParseIP(fields[0])
		if ip == nil || !lan.Contains(ip) {
			continue
		}
		for i := 1; i+1 < len(fields); i++ {
			if fields[i] != "lladdr" {
				continue
			}
			mac, err := normalizeMAC(fields[i+1])
			if err == nil {
				out[mac] = append(out[mac], ip.String())
			}
		}
	}
	for mac, ips := range out {
		sort.Strings(ips)
		out[mac] = unique(ips)
	}
	return out
}
