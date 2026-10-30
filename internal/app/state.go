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
	"sync/atomic"
	"time"

	"github.com/gtoxlili/mihomo-capture/internal/gateway"
	"github.com/gtoxlili/mihomo-capture/internal/module"
)

type diskState struct {
	Devices []Device `json:"devices"`
	Modules []Module `json:"modules"`
}

type gatewayBackend interface {
	Select(context.Context, gateway.Selection) error
	Status(context.Context) (gateway.Status, error)
	Clear(context.Context) error
	DialContext(ctx context.Context, policy, target string) (net.Conn, error)
	Close()
}

type App struct {
	mu          sync.Mutex
	Config      Config
	Fingerprint string
	devices     map[string]Device
	// modules are in creation order, the order their rules apply in.
	modules       []*moduleState
	active        atomic.Pointer[map[string]*module.Module] // enabled modules, read by every request
	slots         []slot                                    // daily lanes by slot
	engine        *module.Engine
	capture       *Capture
	generation    uint64
	epoch         uint64
	store         *Store
	gate          gatewayBackend
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
	a := &App{Config: c, Fingerprint: fingerprint, devices: map[string]Device{}, store: s, gate: gate, self: localMACs()}
	options := module.Options{StorePath: filepath.Join(c.DataDir, "script-store.json")}
	if gate != nil {
		options.Dial = func(ctx context.Context, policy, address string) (net.Conn, error) {
			return gate.DialContext(ctx, gateway.PolicyID(policy), address)
		}
	}
	if a.engine, err = module.NewEngine(options); err != nil {
		return nil, err
	}
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
			compiled, err := module.Compile(m.ID, m.Definition)
			if err != nil {
				return nil, fmt.Errorf("invalid state: module %s: %w", m.ID, err)
			}
			if m.Devices == nil {
				m.Devices = []string{}
			}
			m.Definition = compiled.Definition
			a.modules = append(a.modules, &moduleState{m, compiled})
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	a.publishModulesLocked()
	return a, nil
}

// Close writes the scripts' persistent store.
func (a *App) Close() error { return a.engine.Close() }

func (a *App) snapshotLocked() diskState {
	s := diskState{Devices: []Device{}, Modules: []Module{}}
	for _, d := range a.devices {
		d.Capturing = false
		s.Devices = append(s.Devices, d)
	}
	sort.Slice(s.Devices, func(i, j int) bool { return s.Devices[i].ID < s.Devices[j].ID })
	for _, m := range a.modules {
		s.Modules = append(s.Modules, m.Module)
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
	sel := gateway.Selection{}
	sets := map[string][]string{} // device IPs by module set
	for _, d := range a.devicesLocked() {
		switch {
		case !d.Trusted || !d.Online:
		case d.Capturing:
			sel.CaptureIPs = append(sel.CaptureIPs, d.IPs...)
			sel.BlockUDPPorts = append(sel.BlockUDPPorts, a.capture.BlockUDPPorts...)
		default:
			if key := a.moduleSetLocked(d.ID); key != "" {
				sets[key] = append(sets[key], d.IPs...)
			}
		}
	}
	sel.Daily = a.dailyLanesLocked(sets)
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
