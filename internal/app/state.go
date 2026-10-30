package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gtoxlili/mihomo-capture/internal/gateway"
)

type diskModule struct {
	Module
	Text string `json:"content"`
}
type diskState struct {
	Devices []Device     `json:"devices"`
	Modules []diskModule `json:"modules"`
}

type gatewayBackend interface {
	Select(context.Context, gateway.Selection) error
	Status(context.Context) (gateway.Status, error)
	Clear(context.Context) error
	DialContext(ctx context.Context, policy, target string) (net.Conn, error)
	Close()
}

type App struct {
	mu            sync.Mutex
	Config        Config
	Fingerprint   string
	devices       map[string]Device
	modules       []Module
	capture       *Capture
	generation    uint64
	epoch         uint64
	store         *Store
	gate          gatewayBackend
	processor     Processor
	lastSyncError string
	self          map[string]bool // MACs of this host, the gateway itself
}

func New(c Config, fingerprint string, gate gatewayBackend) (*App, error) {
	if err := os.MkdirAll(c.DataDir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(c.DataDir, 0700); err != nil {
		return nil, err
	}
	s, err := NewStore(filepath.Join(c.DataDir, "records"), c)
	if err != nil {
		return nil, err
	}
	a := &App{Config: c, Fingerprint: fingerprint, devices: map[string]Device{}, store: s, gate: gate, processor: noProcessor{}, self: localMACs()}
	b, err := os.ReadFile(filepath.Join(c.DataDir, "state.json"))
	if err == nil {
		var state diskState
		if err = json.Unmarshal(b, &state); err != nil {
			return nil, fmt.Errorf("invalid state: %w", err)
		}
		for _, d := range state.Devices {
			d.Online = false
			d.Capturing = false
			if d.CAFingerprint != fingerprint {
				d.Trusted = false
			}
			a.devices[d.ID] = d
		}
		for _, m := range state.Modules {
			m.Module.Content = m.Text
			a.modules = append(a.modules, m.Module)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return a, nil
}

func (a *App) snapshotLocked() diskState {
	s := diskState{Devices: []Device{}, Modules: []diskModule{}}
	for _, d := range a.devices {
		d.Capturing = false
		s.Devices = append(s.Devices, d)
	}
	sort.Slice(s.Devices, func(i, j int) bool { return s.Devices[i].ID < s.Devices[j].ID })
	for _, m := range a.modules {
		s.Modules = append(s.Modules, diskModule{m, m.Content})
	}
	return s
}

func atomicJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}

func (a *App) saveLocked() error {
	return atomicJSON(filepath.Join(a.Config.DataDir, "state.json"), a.snapshotLocked())
}

func (a *App) syncLocked(ctx context.Context) error {
	if a.gate == nil {
		return nil
	}
	sel := gateway.Selection{Hostnames: a.hostnamesLocked()}
	for _, d := range a.devicesLocked() {
		switch {
		case !d.Trusted || !d.Online:
		case d.Capturing:
			sel.CaptureIPs = append(sel.CaptureIPs, d.IPs...)
			sel.BlockUDPPorts = append(sel.BlockUDPPorts, a.capture.BlockUDPPorts...)
		default:
			sel.DailyIPs = append(sel.DailyIPs, d.IPs...)
		}
	}
	err := a.gate.Select(ctx, sel)
	if err != nil {
		a.lastSyncError = err.Error()
	} else {
		a.lastSyncError = ""
	}
	return err
}

// Sync republishes the selection; it also restores the capture lanes after the
// gateway reloads its own config.
func (a *App) Sync(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.syncLocked(ctx)
}
func (a *App) ClearGateway(ctx context.Context) error {
	if a.gate == nil {
		return nil
	}
	return a.gate.Clear(ctx)
}

func (a *App) devicesLocked() []Device {
	r := make([]Device, 0, len(a.devices))
	for _, d := range a.devices {
		d.Capturing = a.capture != nil && a.capture.DeviceID == d.ID
		r = append(r, d)
	}
	sort.Slice(r, func(i, j int) bool { return r[i].ID < r[j].ID })
	return r
}

func normalizeMAC(s string) (string, error) {
	m, err := net.ParseMAC(s)
	if err != nil || len(m) != 6 {
		return "", errors.New("a 6-byte MAC address is required")
	}
	return strings.ToLower(m.String()), nil
}

// deviceID accepts the local device only in local mode; gateway devices are
// identified by MAC address.
func (a *App) deviceID(s string) (string, error) {
	if s == localDeviceID && a.Config.Mode != "gateway" {
		return s, nil
	}
	return normalizeMAC(s)
}

func (a *App) UpdateDevice(ctx context.Context, id, label string) error {
	id, err := a.deviceID(id)
	if err != nil {
		return err
	}
	if len(label) > 200 {
		return errors.New("label exceeds 200 characters")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old, exists := a.devices[id]
	d := old
	d.ID = id
	if id != localDeviceID {
		d.MAC = id
	}
	d.Label = label
	a.devices[id] = d
	if err = a.saveLocked(); err != nil {
		if exists {
			a.devices[id] = old
		} else {
			delete(a.devices, id)
		}
	}
	return err
}

func (a *App) Trust(ctx context.Context, id string, trusted bool, evidence string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	d, ok := a.devices[id]
	if !ok {
		return os.ErrNotExist
	}
	if trusted && strings.TrimSpace(evidence) == "" {
		return errors.New("trust_evidence is required: confirm the current CA certificate is installed and fully trusted on this device")
	}
	old := d
	oldCapture := a.capture
	d.Trusted = trusted
	d.TrustEvidence = evidence
	d.CAFingerprint = a.Fingerprint
	a.devices[id] = d
	if !trusted && a.capture != nil && a.capture.DeviceID == id {
		a.capture = nil
		a.generation++
	}
	if err := a.saveLocked(); err != nil {
		a.devices[id] = old
		a.capture = oldCapture
		return err
	}
	if err := a.syncLocked(ctx); err != nil {
		a.devices[id] = old
		a.capture = oldCapture
		_ = a.saveLocked()
		return err
	}
	return nil
}

func (a *App) StartCapture(ctx context.Context, id string, udpPorts []uint16) (*Capture, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ports := append([]uint16{}, udpPorts...)
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	for i, port := range ports {
		if port == 0 {
			return nil, errors.New("UDP block ports must be 1..65535")
		}
		if i > 0 && ports[i-1] == port {
			return nil, errors.New("UDP block ports must be unique")
		}
	}
	d, ok := a.devices[id]
	if !ok {
		return nil, os.ErrNotExist
	}
	if !d.Trusted {
		return nil, errors.New("device is not confirmed to trust the current CA")
	}
	if !d.Online || len(d.IPs) == 0 {
		return nil, errors.New("device has no current source address")
	}
	if a.capture != nil {
		if a.capture.DeviceID == id && samePorts(a.capture.BlockUDPPorts, ports) {
			c := *a.capture
			return &c, nil
		}
		return nil, errors.New("another capture is active; stop it before starting a new one")
	}
	if a.lastSyncError != "" {
		// The capture lane may still hold an earlier device whose connections
		// would be attributed to this capture; confirm it is empty first.
		if err := a.syncLocked(ctx); err != nil {
			return nil, err
		}
	}
	a.generation++
	a.capture = &Capture{DeviceID: id, StartedAt: time.Now().UTC(), Generation: a.generation, BlockUDPPorts: ports}
	if err := a.syncLocked(ctx); err != nil {
		a.capture = nil
		a.generation++
		return nil, err
	}
	c := *a.capture
	return &c, nil
}

func (a *App) StopCapture(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Invalidate in-flight writers even if provider refresh fails.
	a.capture = nil
	a.generation++
	return a.syncLocked(ctx)
}

// A ticket is issued when a capture-lane connection reaches the service and
// is checked on every record written from that connection.
type ticket struct {
	device            string
	generation, epoch uint64
}

func (a *App) captureTicket() ticket {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.capture == nil {
		return ticket{}
	}
	return ticket{a.capture.DeviceID, a.capture.Generation, a.epoch}
}

func (a *App) record(t ticket, r *Record) {
	if t.device == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.capture == nil || a.capture.Generation != t.generation || a.epoch != t.epoch {
		return
	}
	if d := a.devices[t.device]; !d.Trusted || !d.Online {
		return
	}
	r.DeviceID = t.device
	r.Session = t.generation
	if err := a.store.Put(r); err != nil {
		a.store.noteError(err)
	}
}

func (a *App) ClearRecords() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.epoch++
	return a.store.Clear()
}
func (a *App) Prune() error { return a.store.Prune() }

func samePorts(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (a *App) hostnamesLocked() []string {
	// Surge module patch precedence: each module applies to the previous result.
	out := []string{}
	for _, m := range a.modules {
		mode, hosts := moduleHosts(m.Content)
		switch mode {
		case "replace":
			out = append([]string{}, hosts...)
		case "insert":
			out = append(append([]string{}, hosts...), out...)
		default:
			out = append(out, hosts...)
		}
	}
	return out
}

// shouldDecrypt applies to daily-lane connections: the gateway selected the
// host already, and the service rechecks it against the current patch.
func (a *App) shouldDecrypt(host string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return matchHosts(a.hostnamesLocked(), host)
}
