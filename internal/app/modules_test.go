package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gtoxlili/mihomo-capture/internal/gateway"
)

type apiClient struct {
	t   *testing.T
	url string
	key string
}

func (f *fixture) api(t *testing.T) *apiClient {
	t.Helper()
	s := httptest.NewServer(f.a.Routes(f.ca.Leaf, http.NotFoundHandler()))
	t.Cleanup(s.Close)
	return &apiClient{t, s.URL, f.a.Config.APIToken}
}

func (c *apiClient) do(method, path string, body any, out any) int {
	c.t.Helper()
	var r io.Reader
	if s, ok := body.(string); ok {
		r = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.url+path, r)
	req.Header.Set("Authorization", "Bearer "+c.key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

// fixtureModule rewrites the fixture origin's responses and rejects /ads.
func fixtureModule(name string) map[string]any {
	return map[string]any{
		"name":            name,
		"hostnames":       []string{"-blocked.example", "127.0.0.1"},
		"url_rewrites":    []map[string]any{{"pattern": `/ads$`, "type": "reject"}},
		"header_rewrites": []map[string]any{{"type": "http-response", "pattern": "^https://127", "action": "header-add", "name": "X-Module", "value": name}},
		"scripts": []map[string]any{{"name": "upper", "type": "http-response", "pattern": "/page", "requires_body": true,
			"code": `$done({body: $response.body.toUpperCase()})`}},
	}
}

// contract: docs/api.md — modules are created disabled, validated when
// written, and enabled for chosen devices through their activation.
// invariant: I4 — only trusted devices a module is activated on take its daily
// lane, limited to its hostnames.
func TestModuleLifecycle(t *testing.T) {
	f := newFixture(t)
	c := f.api(t)
	ctx := context.Background()
	for name, body := range map[string]any{
		"bad pattern":   map[string]any{"name": "x", "url_rewrites": []map[string]any{{"pattern": "(", "replacement": "x"}}},
		"unknown field": `{"name":"x","source":"https://example.com/module"}`,
		"script syntax": map[string]any{"name": "x", "scripts": []map[string]any{{"name": "s", "pattern": ".", "code": "$done({"}}},
	} {
		var e struct {
			Error struct{ Message string }
		}
		if status := c.do("POST", "/v1/modules", body, &e); status != 400 || e.Error.Message == "" {
			t.Errorf("%s: %d %+v", name, status, e)
		}
	}
	// Definitions carry scripts beyond the other routes' 64 KiB.
	big := fixtureModule("Big")
	big["scripts"] = []map[string]any{{"name": "s", "pattern": ".", "code": "$done()//" + strings.Repeat("x", 100<<10)}}
	var m Module
	if status := c.do("POST", "/v1/modules", big, &m); status != 201 || m.Enabled || len(m.Devices) != 0 || m.Scripts[0].Timeout != 5 {
		t.Fatalf("create: %d %+v", status, m)
	}
	if status := c.do("PUT", "/v1/modules/"+m.ID, fixtureModule("Fixture"), &m); status != 200 || m.Name != "Fixture" {
		t.Fatalf("update: %d %+v", status, m)
	}
	if sel := f.gate.published(); len(sel.Daily) != 0 {
		t.Fatalf("a disabled module was published: %+v", sel)
	}
	if status := c.do("PUT", "/v1/modules/"+m.ID+"/activation", map[string]any{"enabled": true, "devices": []string{"phone"}}, nil); status != 400 {
		t.Fatalf("unknown device: %d", status)
	}
	if status := c.do("PUT", "/v1/modules/nope/activation", map[string]any{"enabled": true}, nil); status != 404 {
		t.Fatalf("unknown module: %d", status)
	}
	// otherMAC is not trusted: it is listed but takes no lane.
	if status := c.do("PUT", "/v1/modules/"+m.ID+"/activation", map[string]any{"enabled": true, "devices": []string{"00-11-22-33-44-55", otherMAC}}, &m); status != 200 || strings.Join(m.Devices, ",") != selectedMAC+","+otherMAC {
		t.Fatalf("activate: %d %+v", status, m)
	}
	sel := f.gate.published()
	if len(sel.Daily) != 1 || strings.Join(sel.Daily[0].IPs, ",") != "192.0.2.10" || strings.Join(sel.Daily[0].Hostnames[0], ",") != "-blocked.example,127.0.0.1" {
		t.Fatalf("published selection %+v", sel)
	}
	var list struct{ Items []ModuleSummary }
	if c.do("GET", "/v1/modules", nil, &list); len(list.Items) != 1 || list.Items[0].Rules["scripts"] != 1 || !list.Items[0].Enabled {
		t.Fatalf("list: %+v", list)
	}

	// The daily lane decrypts the module's hosts and applies it, recording nothing.
	origin := f.origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "page") }))
	daily := f.client(t, "daily-0")
	get := func(c *http.Client, path string) (*http.Response, string) {
		t.Helper()
		res, err := c.Get(origin.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, string(b)
	}
	if res, body := get(daily, "/page"); body != "PAGE" || res.Header.Get("X-Module") != "Fixture" || bytes.Equal(res.TLS.PeerCertificates[0].Raw, f.leaf.Certificate[0]) {
		t.Fatalf("daily processing: %q %v", body, res.Header)
	}
	if res, _ := get(daily, "/ads"); res.StatusCode != 404 {
		t.Fatalf("reject: %d", res.StatusCode)
	}
	if n := len(records(t, f.a, false)); n != 0 {
		t.Fatalf("daily lane recorded %d requests", n)
	}

	// A second module changes the device's set: it moves to another slot, and
	// the old one keeps answering for connections already routed to it.
	var second Module
	c.do("POST", "/v1/modules", map[string]any{"name": "Second", "hostnames": []string{"*.example"}}, &second)
	c.do("PUT", "/v1/modules/"+second.ID+"/activation", map[string]any{"enabled": true, "devices": []string{selectedMAC}}, nil)
	sel = f.gate.published()
	if len(sel.Daily) != 2 || len(sel.Daily[0].IPs) != 0 || strings.Join(sel.Daily[1].IPs, ",") != "192.0.2.10" || len(sel.Daily[1].Hostnames) != 2 {
		t.Fatalf("published after the set changed: %+v", sel)
	}
	if _, scope, _ := f.a.laneScope("daily-0"); strings.Join(scope, ",") != m.ID {
		t.Fatalf("retired slot scope: %v", scope)
	}
	if res, body := get(daily, "/page"); body != "PAGE" || res.Header.Get("X-Module") != "Fixture" {
		t.Fatal("an open connection lost its module set")
	}
	// Disabling takes effect on the next request, even on an open connection.
	c.do("PUT", "/v1/modules/"+m.ID+"/activation", map[string]any{"enabled": false}, &m)
	if _, body := get(daily, "/page"); body != "page" || strings.Join(m.Devices, ",") != selectedMAC+","+otherMAC {
		t.Fatalf("disabled module still applied: %q, devices %v", body, m.Devices)
	}
	f.a.mu.Lock()
	f.a.slots[0].retired = time.Now().Add(-slotGrace)
	f.a.mu.Unlock()
	if _, scope, _ := f.a.laneScope("daily-0"); scope != nil {
		t.Fatalf("slot past its grace still has a set: %v", scope)
	}

	// Modules survive a restart.
	restarted, err := New(f.a.Config, f.a.Fingerprint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.Module(second.ID); err != nil || !got.Enabled || got.Devices[0] != selectedMAC {
		t.Fatalf("after restart: %+v %v", got, err)
	}
	_ = restarted.Close()
	if status := c.do("DELETE", "/v1/modules/"+second.ID, nil, nil); status != 200 {
		t.Fatalf("delete: %d", status)
	}
	if status := c.do("GET", "/v1/modules/"+second.ID, nil, nil); status != 404 {
		t.Fatalf("deleted module: %d", status)
	}
	_ = ctx
}

// contract: README.md — modules act on the captured device too; its records
// show what the device sent and received, and name the modules that acted.
func TestModulesDuringCapture(t *testing.T) {
	f := newFixture(t)
	c := f.api(t)
	var m Module
	def := fixtureModule("Fixture")
	def["scripts"] = append(def["scripts"].([]map[string]any),
		map[string]any{"name": "boom", "pattern": "/boom", "code": `null.x`},
		map[string]any{"name": "cut", "type": "http-request", "pattern": "/cut", "code": `$done({abort: true})`})
	c.do("POST", "/v1/modules", def, &m)
	c.do("PUT", "/v1/modules/"+m.ID+"/activation", map[string]any{"enabled": true, "devices": []string{selectedMAC}}, nil)
	if _, err := f.a.StartCapture(context.Background(), selectedMAC, nil); err != nil {
		t.Fatal(err)
	}
	if sel := f.gate.published(); len(sel.CaptureIPs) != 1 || len(sel.Daily) != 0 && len(sel.Daily[0].IPs) != 0 {
		t.Fatalf("the captured device must take only the capture lane: %+v", sel)
	}
	origin := f.origin(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "page") }))
	capture := f.client(t, gateway.LaneCapture)
	for _, path := range []string{"/page", "/boom"} {
		res, err := capture.Get(origin.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(res.Body)
		res.Body.Close()
	}
	// contract: docs/api.md — $done({abort: true}) closes the connection.
	if res, err := capture.Get(origin.URL + "/cut"); err == nil {
		res.Body.Close()
		t.Fatalf("an aborted request was answered: %d", res.StatusCode)
	}
	var page, boom, cut *Record
	deadline := time.Now().Add(5 * time.Second)
	for page == nil || boom == nil || cut == nil {
		for _, r := range records(t, f.a, false) {
			switch {
			case !r.Complete:
			case strings.HasSuffix(r.URL, "/page"):
				page = &r
			case strings.HasSuffix(r.URL, "/boom"):
				boom = &r
			case strings.HasSuffix(r.URL, "/cut"):
				cut = &r
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("records missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if page.Response.Body.Data != "PAGE" || page.Response.Headers.Get("X-Module") != "Fixture" || strings.Join(page.Modules, ",") != "Fixture" {
		t.Fatalf("record of a processed request: %+v", page)
	}
	// The failing script passed the response through; header rewrites still applied.
	if boom.Response.Body.Data != "page" || boom.Status != 200 {
		t.Fatalf("record of a failed script: %+v", boom)
	}
	if cut.Status != 0 || cut.Error != "connection closed by a module script" || strings.Join(cut.Modules, ",") != "Fixture" {
		t.Fatalf("record of an aborted request: %+v", cut)
	}
	var logs struct {
		Items []struct{ Level, Source, Message string }
	}
	if c.do("GET", "/v1/modules/"+m.ID+"/logs", nil, &logs); len(logs.Items) == 0 || logs.Items[0].Source != "boom" || logs.Items[0].Level != "error" {
		t.Fatalf("logs: %+v", logs)
	}
}
