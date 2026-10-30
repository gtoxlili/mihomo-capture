package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gtoxlili/mihomo-capture/internal/gateway"
	"github.com/gtoxlili/mihomo-capture/internal/module"
)

const maxModules = 64

// Module is a module as the API shows it and the state file keeps it: its
// definition and where it acts.
type Module struct {
	ID string `json:"id"`
	// Enabled modules act on the trusted devices listed in Devices.
	Enabled   bool      `json:"enabled"`
	Devices   []string  `json:"devices"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	module.Definition
}

// ModuleSummary is a module in the list: everything but its rules' content.
type ModuleSummary struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Enabled   bool           `json:"enabled"`
	Devices   []string       `json:"devices"`
	Hostnames []string       `json:"hostnames"`
	Rules     map[string]int `json:"rules"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type moduleState struct {
	Module
	compiled *module.Module
}

func (m *moduleState) summary() ModuleSummary {
	d := m.Definition
	return ModuleSummary{ID: m.ID, Name: d.Name, Enabled: m.Enabled, Devices: m.Devices, Hostnames: d.Hostnames, UpdatedAt: m.UpdatedAt, Rules: map[string]int{
		"header_rewrites": len(d.HeaderRewrites), "url_rewrites": len(d.URLRewrites), "map_locals": len(d.MapLocals), "body_rewrites": len(d.BodyRewrites), "scripts": len(d.Scripts),
	}}
}

// slot is a daily lane's module set. A set no device uses any more stays
// retired for slotGrace before the slot takes another set: a connection the
// gateway routed just before the change still finds the set it was routed for.
type slot struct {
	key     string // module IDs joined by ","
	retired time.Time
}

const slotGrace = 30 * time.Second

func (s slot) live() bool { return s.key != "" && s.retired.IsZero() }

func (a *App) Modules() []ModuleSummary {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ModuleSummary, 0, len(a.modules))
	for _, m := range a.modules {
		out = append(out, m.summary())
	}
	return out
}

func (a *App) Module(id string) (Module, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i := a.moduleIndexLocked(id); i >= 0 {
		return a.modules[i].Module, nil
	}
	return Module{}, os.ErrNotExist
}

func (a *App) ModuleLogs(id string) ([]module.LogEntry, error) {
	if _, err := a.Module(id); err != nil {
		return nil, err
	}
	return a.engine.Logs(id), nil
}

func (a *App) moduleIndexLocked(id string) int {
	return slices.IndexFunc(a.modules, func(m *moduleState) bool { return m.ID == id })
}

// CreateModule adds a module, disabled and without devices.
func (a *App) CreateModule(d module.Definition) (Module, error) {
	id := newID()
	c, err := module.Compile(id, d)
	if err != nil {
		return Module{}, err
	}
	now := time.Now().UTC()
	m := &moduleState{Module{ID: id, Devices: []string{}, CreatedAt: now, UpdatedAt: now, Definition: c.Definition}, c}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.modules) >= maxModules {
		return Module{}, fmt.Errorf("at most %d modules", maxModules)
	}
	a.modules = append(a.modules, m)
	if err = a.saveLocked(); err != nil {
		a.modules = a.modules[:len(a.modules)-1]
		return Module{}, err
	}
	return m.Module, nil
}

// UpdateModule replaces a module's definition. Requests that follow use it,
// including those on connections already open.
func (a *App) UpdateModule(ctx context.Context, id string, d module.Definition) (Module, error) {
	c, err := module.Compile(id, d)
	if err != nil {
		return Module{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.moduleIndexLocked(id)
	if i < 0 {
		return Module{}, os.ErrNotExist
	}
	m := a.modules[i]
	old := *m
	m.Definition, m.compiled, m.UpdatedAt = c.Definition, c, time.Now().UTC()
	rerouted := m.Enabled && !slices.Equal(old.Hostnames, m.Hostnames)
	if err = a.commitModulesLocked(ctx, rerouted, func() { *m = old }); err != nil {
		return Module{}, err
	}
	return m.Module, nil
}

// ActivateModule enables or disables a module and, when devices is not nil,
// sets the devices it acts on.
func (a *App) ActivateModule(ctx context.Context, id string, enabled bool, devices *[]string) (Module, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.moduleIndexLocked(id)
	if i < 0 {
		return Module{}, os.ErrNotExist
	}
	m := a.modules[i]
	old := *m
	if devices != nil {
		list := []string{}
		for _, raw := range *devices {
			d, err := a.deviceID(raw)
			if err != nil {
				return Module{}, invalidInput{fmt.Errorf("devices: %q: %w", raw, err)}
			}
			if _, ok := a.devices[d]; !ok {
				return Module{}, invalidInput{fmt.Errorf("devices: %q is not a known device", raw)}
			}
			list = append(list, d)
		}
		sort.Strings(list)
		m.Devices = slices.Compact(list)
	}
	m.Enabled = enabled
	m.UpdatedAt = time.Now().UTC()
	if sets := a.moduleSetsLocked(); sets > gateway.MaxDailyLanes {
		*m = old
		return Module{}, fmt.Errorf("devices would use %d distinct module sets; at most %d can be active", sets, gateway.MaxDailyLanes)
	}
	if err := a.commitModulesLocked(ctx, true, func() { *m = old }); err != nil {
		return Module{}, err
	}
	return m.Module, nil
}

func (a *App) RemoveModule(ctx context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.moduleIndexLocked(id)
	if i < 0 {
		return os.ErrNotExist
	}
	old := append([]*moduleState{}, a.modules...)
	enabled := a.modules[i].Enabled
	a.modules = slices.Delete(a.modules, i, i+1)
	if err := a.commitModulesLocked(ctx, enabled, func() { a.modules = old }); err != nil {
		return err
	}
	a.engine.Forget(id)
	return nil
}

// commitModulesLocked persists a module change, makes requests see it, and
// republishes the lanes when routing changed; on failure it undoes the change.
func (a *App) commitModulesLocked(ctx context.Context, rerouted bool, undo func()) error {
	err := a.saveLocked()
	if err == nil {
		a.publishModulesLocked()
		if rerouted {
			err = a.syncLocked(ctx)
		}
		if err != nil {
			undo()
			a.publishModulesLocked()
			_ = a.saveLocked()
		}
		return err
	}
	undo()
	return err
}

// publishModulesLocked replaces the table of enabled modules that requests
// read without taking the lock.
func (a *App) publishModulesLocked() {
	active := map[string]*module.Module{}
	for _, m := range a.modules {
		if m.Enabled {
			active[m.ID] = m.compiled
		}
	}
	a.active.Store(&active)
}

// moduleSetLocked is the key of the enabled modules acting on a device, in
// module order, which is the order their rules apply in.
func (a *App) moduleSetLocked(device string) string {
	var ids []string
	for _, m := range a.modules {
		if m.Enabled && len(m.Hostnames) > 0 && slices.Contains(m.Devices, device) {
			ids = append(ids, m.ID)
		}
	}
	return strings.Join(ids, ",")
}

func (a *App) moduleSetsLocked() int {
	sets := map[string]bool{}
	for id, d := range a.devices {
		if key := a.moduleSetLocked(id); key != "" && d.Trusted {
			sets[key] = true
		}
	}
	return len(sets)
}

// assignSlotsLocked gives each module set a daily lane. A set keeps its slot
// while it is in use, so reassignments happen only on module changes.
func (a *App) assignSlotsLocked(sets map[string][]string, now time.Time) {
	pending := map[string]bool{}
	for key := range sets {
		pending[key] = true
	}
	for i := range a.slots {
		s := &a.slots[i]
		switch {
		case s.key == "":
		case pending[s.key]:
			s.retired = time.Time{}
			delete(pending, s.key)
		case s.retired.IsZero():
			s.retired = now
		case now.Sub(s.retired) >= slotGrace:
			*s = slot{}
		}
	}
	keys := make([]string, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if i := slices.IndexFunc(a.slots, func(s slot) bool { return s.key == "" }); i >= 0 {
			a.slots[i] = slot{key: key}
		} else {
			a.slots = append(a.slots, slot{key: key})
		}
	}
	for len(a.slots) > 0 && a.slots[len(a.slots)-1].key == "" {
		a.slots = a.slots[:len(a.slots)-1]
	}
}

// dailyLanesLocked is the daily part of the selection, one lane per slot.
func (a *App) dailyLanesLocked(sets map[string][]string) []gateway.DailyLane {
	a.assignSlotsLocked(sets, time.Now())
	lanes := make([]gateway.DailyLane, len(a.slots))
	for i, s := range a.slots {
		if !s.live() {
			continue
		}
		lanes[i].IPs = sets[s.key]
		for _, id := range strings.Split(s.key, ",") {
			if j := a.moduleIndexLocked(id); j >= 0 {
				lanes[i].Hostnames = append(lanes[i].Hostnames, a.modules[j].Hostnames)
			}
		}
	}
	return lanes
}

// laneScope resolves a lane connection when it arrives: the capture lane
// gets a record ticket and the selected device's modules, a daily lane the
// module set its slot was published with.
func (a *App) laneScope(lane string) (ticket, []string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if lane == gateway.LaneCapture {
		if a.capture == nil {
			return ticket{}, nil, nil
		}
		t := ticket{a.capture.DeviceID, a.capture.Generation, a.epoch}
		return t, splitSet(a.moduleSetLocked(t.device)), nil
	}
	n, ok := gateway.DailySlot(lane)
	if !ok {
		return ticket{}, nil, errors.New("unknown lane")
	}
	if n >= len(a.slots) {
		return ticket{}, nil, nil
	}
	s := a.slots[n]
	if !s.retired.IsZero() && time.Since(s.retired) >= slotGrace {
		return ticket{}, nil, nil
	}
	return ticket{}, splitSet(s.key), nil
}

func splitSet(key string) []string {
	if key == "" {
		return nil
	}
	return strings.Split(key, ",")
}

// modulesFor returns the scope's modules that are enabled now and act on the
// host. Disabling or editing a module takes effect on the next request.
func (a *App) modulesFor(scope []string, host string) []*module.Module {
	if len(scope) == 0 {
		return nil
	}
	active := *a.active.Load()
	var out []*module.Module
	for _, id := range scope {
		if m := active[id]; m != nil && m.MatchHost(host) {
			out = append(out, m)
		}
	}
	return out
}

// invalidInput is an argument error, reported as a bad request.
type invalidInput struct{ error }
