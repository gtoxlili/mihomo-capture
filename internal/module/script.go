package module

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/net/http/httpguts"
)

// Script runtime limits.
const (
	maxTimers       = 64
	maxTimerDelay   = 24 * time.Hour
	maxHTTPRequests = 20 // concurrent $httpClient requests per run
	maxHTTPBody     = 32 << 20
	httpTimeout     = 5 * time.Second
	maxCallStack    = 4096
)

// headerProxy makes a header object answer to any letter case, as HTTP
// header names do; the object keeps the names it was given.
var headerProxy = goja.MustCompile("headers", `(function (t) {
	const find = (k) => typeof k === "string" && !(k in t) ? Object.keys(t).find((x) => x.toLowerCase() === k.toLowerCase()) : k;
	return new Proxy(t, {
		get: (o, k) => o[find(k) ?? k],
		has: (o, k) => find(k) !== undefined && find(k) in o,
		set: (o, k, v) => { o[find(k) ?? k] = v; return true },
		deleteProperty: (o, k) => { const f = find(k); if (f !== undefined) delete o[f]; return true },
	});
})`, true)

// call is a script's input.
type call struct {
	kind    string
	id      string
	req     requestView
	status  int         // http-response only
	header  http.Header // http-response only
	body    []byte
	hasBody bool
}

// result is what $done asked for; zero fields leave the message unchanged.
type result struct {
	url      string
	header   http.Header
	body     []byte
	hasBody  bool
	status   int
	abort    bool
	response *local
}

type local struct {
	status int
	header http.Header
	body   []byte
}

type run struct {
	e       *Engine
	m       *Module
	s       *script
	vm      *goja.Runtime
	ctx     context.Context
	target  string
	session string
	jobs    chan func() error
	pending int
	timers  map[int64]*time.Timer
	timerID int64
	http    chan struct{}
	done    bool
	value   goja.Value
}

// run executes one script within its timeout. ok is false when the script
// failed, timed out or never called $done; the message then stays as it was.
func (e *Engine) run(parent context.Context, m *Module, s *script, in call) (out result, ok bool) {
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	r := &run{e: e, m: m, s: s, ctx: ctx, target: in.req.url, session: session(), jobs: make(chan func() error, 16), timers: map[int64]*time.Timer{}, http: make(chan struct{}, maxHTTPRequests)}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		e.note(m, "error", s.Name, r.target, "[%s] timed out waiting for a free script slot", r.session)
		return out, false
	}
	r.vm = goja.New()
	r.vm.SetMaxCallStackSize(maxCallStack)
	stop := context.AfterFunc(ctx, func() { r.vm.Interrupt(ctx.Err()) })
	defer stop()
	defer func() {
		for _, t := range r.timers {
			t.Stop()
		}
	}()
	if err := r.globals(in); err != nil {
		e.note(m, "error", s.Name, r.target, "[%s] %v", r.session, err)
		return out, false
	}
	_, err := r.vm.RunProgram(s.program)
	for err == nil && !r.done && r.pending > 0 {
		select {
		case job := <-r.jobs:
			r.pending--
			err = job()
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	if err != nil {
		var interrupted *goja.InterruptedError
		switch {
		case parent.Err() != nil:
			e.note(m, "warn", s.Name, r.target, "[%s] stopped: the request ended first", r.session)
		case errors.As(err, &interrupted) || errors.Is(err, context.DeadlineExceeded):
			e.note(m, "error", s.Name, r.target, "[%s] timed out after %s; the message passes unchanged", r.session, s.timeout)
		default:
			e.note(m, "error", s.Name, r.target, "[%s] %v; the message passes unchanged", r.session, err)
		}
		return out, false
	}
	if !r.done {
		e.note(m, "warn", s.Name, r.target, "[%s] finished without calling $done; the message passes unchanged", r.session)
		return out, false
	}
	out, err = r.result(in)
	if err != nil {
		e.note(m, "error", s.Name, r.target, "[%s] $done: %v; the message passes unchanged", r.session, err)
		return result{}, false
	}
	return out, true
}

func session() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// post hands a callback to the run's goroutine, where the VM lives.
func (r *run) post(job func() error) {
	select {
	case r.jobs <- job:
	case <-r.ctx.Done():
	}
}

func (r *run) globals(in call) error {
	vm := r.vm
	set := func(name string, v any) {
		if err := vm.Set(name, v); err != nil {
			panic(err)
		}
	}
	wrap, err := vm.RunProgram(headerProxy)
	if err != nil {
		return err
	}
	headers, _ := goja.AssertFunction(wrap)
	toHeaders := func(h http.Header, host string) (goja.Value, error) {
		names := make([]string, 0, len(h))
		for k := range h {
			names = append(names, k)
		}
		sort.Strings(names)
		if r.s.FullHeaderMode {
			list := []any{}
			if host != "" {
				list = append(list, map[string]any{"field": "Host", "value": host})
			}
			for _, k := range names {
				for _, v := range h[k] {
					list = append(list, map[string]any{"field": k, "value": v})
				}
			}
			return vm.ToValue(list), nil
		}
		o := vm.NewObject()
		if host != "" {
			_ = o.Set("Host", host)
		}
		for _, k := range names {
			_ = o.Set(k, strings.Join(h[k], ", "))
		}
		return headers(goja.Undefined(), o)
	}
	req := vm.NewObject()
	_ = req.Set("url", in.req.url)
	_ = req.Set("method", in.req.method)
	_ = req.Set("id", in.id)
	h, err := toHeaders(in.req.header, in.req.host)
	if err != nil {
		return err
	}
	_ = req.Set("headers", h)
	if in.kind == HTTPRequest && in.hasBody {
		_ = req.Set("body", r.bodyValue(in.body))
	}
	set("$request", req)
	if in.kind == HTTPResponse {
		res := vm.NewObject()
		_ = res.Set("status", in.status)
		h, err := toHeaders(in.header, "")
		if err != nil {
			return err
		}
		_ = res.Set("headers", h)
		if in.hasBody {
			_ = res.Set("body", r.bodyValue(in.body))
		}
		set("$response", res)
	}
	set("$done", func(c goja.FunctionCall) goja.Value {
		// Calls after the first are ignored.
		if !r.done {
			r.done, r.value = true, c.Argument(0)
		}
		return goja.Undefined()
	})
	if r.s.Argument != "" {
		set("$argument", r.s.Argument)
	}
	set("$script", map[string]any{"name": r.s.Name, "type": r.s.Type, "startTime": float64(time.Now().UnixMilli()) / 1000, "sessionID": r.session, "binaryBodyMode": r.s.BinaryBodyMode})
	system := map[string]string{"darwin": "macOS", "linux": "Linux", "windows": "Windows"}[runtime.GOOS]
	set("$environment", map[string]any{"system": system, "language": "en"})
	set("console", r.console())
	set("setTimeout", r.setTimeout)
	set("clearTimeout", r.clearTimeout)
	set("$persistentStore", map[string]any{"read": r.storeRead, "write": r.storeWrite})
	set("$notification", map[string]any{"post": func(c goja.FunctionCall) goja.Value {
		parts := []string{}
		for _, a := range c.Arguments[:min(3, len(c.Arguments))] {
			if s := r.text(a); s != "" {
				parts = append(parts, s)
			}
		}
		r.e.note(r.m, "notify", r.s.Name, r.target, "%s", strings.Join(parts, " — "))
		return goja.Undefined()
	}})
	set("$utils", map[string]any{"ungzip": func(c goja.FunctionCall) goja.Value {
		b, ok := r.bytesOf(c.Argument(0))
		if !ok {
			return goja.Null()
		}
		z, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return goja.Null()
		}
		out, err := io.ReadAll(io.LimitReader(z, maxHTTPBody+1))
		if err != nil || len(out) > maxHTTPBody {
			return goja.Null()
		}
		return r.uint8Array(out)
	}})
	client := vm.NewObject()
	for _, method := range []string{"get", "post", "put", "delete", "head", "options", "patch"} {
		_ = client.Set(method, r.httpMethod(strings.ToUpper(method)))
	}
	set("$httpClient", client)
	return nil
}

func (r *run) bodyValue(b []byte) goja.Value {
	if r.s.BinaryBodyMode {
		return r.uint8Array(b)
	}
	return r.vm.ToValue(string(b))
}

func (r *run) uint8Array(b []byte) goja.Value {
	ctor := r.vm.Get("Uint8Array")
	v, err := r.vm.New(ctor, r.vm.ToValue(r.vm.NewArrayBuffer(bytes.Clone(b))))
	if err != nil {
		panic(err)
	}
	return v
}

// bytesOf accepts the body types scripts may give: a string, an ArrayBuffer
// or any typed array.
func (r *run) bytesOf(v goja.Value) ([]byte, bool) {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, false
	}
	switch x := v.Export().(type) {
	case string:
		return []byte(x), true
	case []byte:
		return bytes.Clone(x), true
	case goja.ArrayBuffer:
		return bytes.Clone(x.Bytes()), true
	}
	if o, ok := v.(*goja.Object); ok {
		if buf, ok := o.Get("buffer").Export().(goja.ArrayBuffer); ok {
			off, n := o.Get("byteOffset").ToInteger(), o.Get("byteLength").ToInteger()
			if b := buf.Bytes(); off >= 0 && n >= 0 && off+n <= int64(len(b)) {
				return bytes.Clone(b[off : off+n]), true
			}
		}
	}
	return []byte(v.String()), true
}

// text is how console and notifications print a value: objects as JSON.
func (r *run) text(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) {
		return "undefined"
	}
	if o, ok := v.(*goja.Object); ok {
		if _, isFunc := goja.AssertFunction(o); !isFunc {
			if b, err := o.MarshalJSON(); err == nil {
				return string(b)
			}
		}
	}
	return v.String()
}

func (r *run) console() map[string]any {
	print := func(level string) func(goja.FunctionCall) goja.Value {
		return func(c goja.FunctionCall) goja.Value {
			parts := make([]string, len(c.Arguments))
			for i, a := range c.Arguments {
				if s, ok := a.Export().(string); ok {
					parts[i] = s
				} else {
					parts[i] = r.text(a)
				}
			}
			r.e.note(r.m, level, r.s.Name, r.target, "[%s] %s", r.session, strings.Join(parts, " "))
			return goja.Undefined()
		}
	}
	return map[string]any{"log": print("info"), "info": print("info"), "debug": print("info"), "warn": print("warn"), "error": print("error")}
}

func (r *run) setTimeout(c goja.FunctionCall) goja.Value {
	fn, ok := goja.AssertFunction(c.Argument(0))
	if !ok {
		panic(r.vm.NewTypeError("setTimeout requires a function"))
	}
	if len(r.timers) >= maxTimers {
		panic(r.vm.NewGoError(fmt.Errorf("at most %d timers may be pending", maxTimers)))
	}
	delay := min(max(time.Duration(c.Argument(1).ToInteger())*time.Millisecond, 0), maxTimerDelay)
	args := append([]goja.Value{}, c.Arguments[min(2, len(c.Arguments)):]...)
	r.timerID++
	id := r.timerID
	r.pending++
	r.timers[id] = time.AfterFunc(delay, func() {
		r.post(func() error {
			if _, live := r.timers[id]; !live {
				return nil // cleared after it fired
			}
			delete(r.timers, id)
			_, err := fn(goja.Undefined(), args...)
			return err
		})
	})
	return r.vm.ToValue(id)
}

func (r *run) clearTimeout(c goja.FunctionCall) goja.Value {
	id := c.Argument(0).ToInteger()
	if t, ok := r.timers[id]; ok {
		delete(r.timers, id)
		if t.Stop() {
			r.pending--
		}
	}
	return goja.Undefined()
}

func (r *run) storeKey(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) || v.String() == "" {
		return scriptKey(r.m.ID, r.s.Name)
	}
	return sharedKey(v.String())
}

func (r *run) storeRead(c goja.FunctionCall) goja.Value {
	if v, ok := r.e.store.read(r.storeKey(c.Argument(0))); ok {
		return r.vm.ToValue(v)
	}
	return goja.Null()
}

func (r *run) storeWrite(c goja.FunctionCall) goja.Value {
	key := r.storeKey(c.Argument(1))
	if v := c.Argument(0); goja.IsNull(v) || goja.IsUndefined(v) {
		return r.vm.ToValue(r.e.store.write(key, nil))
	}
	s := c.Argument(0).String()
	return r.vm.ToValue(r.e.store.write(key, &s))
}

// httpMethod is one $httpClient method: (options, callback), where options is
// a URL or {url, headers, body, timeout, policy, insecure, auto-redirect,
// binary-mode, full-header-mode} and callback receives (error, response, data).
func (r *run) httpMethod(method string) func(goja.FunctionCall) goja.Value {
	return func(c goja.FunctionCall) goja.Value {
		callback, _ := goja.AssertFunction(c.Argument(1))
		o := map[string]goja.Value{}
		if opts, ok := c.Argument(0).(*goja.Object); ok {
			for _, k := range opts.Keys() {
				o[k] = opts.Get(k)
			}
		} else {
			o["url"] = c.Argument(0)
		}
		flag := func(name string, def bool) bool {
			if v, ok := o[name]; ok && !goja.IsUndefined(v) && !goja.IsNull(v) {
				return v.ToBoolean()
			}
			return def
		}
		binary, full := flag("binary-mode", false), flag("full-header-mode", false)
		req, err := r.httpRequest(method, o)
		if err != nil {
			panic(r.vm.NewGoError(err))
		}
		timeout := httpTimeout
		if v, ok := o["timeout"]; ok && !goja.IsUndefined(v) && v.ToFloat() > 0 {
			timeout = time.Duration(v.ToFloat() * float64(time.Second))
		}
		policy := ""
		if v, ok := o["policy"]; ok && !goja.IsUndefined(v) && !goja.IsNull(v) {
			policy = v.String()
		}
		client := &http.Client{Transport: r.e.transport(policy, flag("insecure", false)), Timeout: timeout}
		if !flag("auto-redirect", true) {
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		}
		r.pending++
		go func() {
			status, header, data, err := r.fetch(client, req)
			r.post(func() error {
				if callback == nil {
					return nil
				}
				if err != nil {
					_, err := callback(goja.Undefined(), r.vm.ToValue(err.Error()), goja.Null(), goja.Null())
					return err
				}
				res := r.vm.NewObject()
				_ = res.Set("status", status)
				if full {
					list := []any{}
					for k, vs := range header {
						for _, v := range vs {
							list = append(list, map[string]any{"field": k, "value": v})
						}
					}
					_ = res.Set("headers", list)
				} else {
					h := map[string]any{}
					for k, vs := range header {
						h[k] = strings.Join(vs, ", ")
					}
					_ = res.Set("headers", h)
				}
				body := r.vm.ToValue(string(data))
				if binary {
					body = r.uint8Array(data)
				}
				_, err := callback(goja.Undefined(), goja.Null(), res, body)
				return err
			})
		}()
		return goja.Undefined()
	}
}

func (r *run) httpRequest(method string, o map[string]goja.Value) (*http.Request, error) {
	raw, ok := o["url"]
	if !ok || goja.IsUndefined(raw) || goja.IsNull(raw) {
		return nil, errors.New("$httpClient requires a url")
	}
	var body io.Reader
	contentType := ""
	if v, ok := o["body"]; ok && !goja.IsUndefined(v) && !goja.IsNull(v) {
		if obj, isObj := v.(*goja.Object); isObj && obj.ClassName() == "Object" {
			// A plain object is sent as JSON.
			b, err := obj.MarshalJSON()
			if err != nil {
				return nil, err
			}
			body, contentType = bytes.NewReader(b), "application/json"
		} else {
			b, _ := r.bytesOf(v)
			body = bytes.NewReader(b)
		}
	}
	req, err := http.NewRequestWithContext(r.ctx, method, raw.String(), body)
	if err != nil {
		return nil, err
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, errors.New("$httpClient url must be http or https")
	}
	if h, ok := o["headers"].(*goja.Object); ok {
		for _, k := range h.Keys() {
			v := h.Get(k).String()
			if httpguts.ValidHeaderFieldName(k) && httpguts.ValidHeaderFieldValue(v) && !framing(k) {
				if strings.EqualFold(k, "Host") {
					req.Host = v
				} else {
					req.Header.Add(k, v)
				}
			}
		}
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

func (r *run) fetch(client *http.Client, req *http.Request) (int, http.Header, []byte, error) {
	select {
	case r.http <- struct{}{}:
		defer func() { <-r.http }()
	case <-r.ctx.Done():
		return 0, nil, nil, r.ctx.Err()
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxHTTPBody+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(data) > maxHTTPBody {
		return 0, nil, nil, fmt.Errorf("response body exceeds %d bytes", maxHTTPBody)
	}
	return res.StatusCode, res.Header, data, nil
}

// transport leaves directly from this host, or through a gateway policy.
func (e *Engine) transport(policy string, insecure bool) http.RoundTripper {
	if policy == "" {
		if insecure {
			return e.insecure
		}
		return e.direct
	}
	t := e.direct.Clone()
	t.TLSClientConfig.InsecureSkipVerify = insecure
	t.DisableKeepAlives = true
	t.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		if e.dial == nil {
			return nil, errors.New("policy requires the gateway")
		}
		return e.dial(ctx, policy, address)
	}
	return t
}

// result reads the value given to $done.
func (r *run) result(in call) (result, error) {
	var out result
	o, ok := r.value.(*goja.Object)
	if !ok {
		return out, nil // $done() or a non-object: unchanged
	}
	field := func(name string) goja.Value {
		v := o.Get(name)
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			return nil
		}
		return v
	}
	if v := field("abort"); v != nil && v.ToBoolean() {
		out.abort = true
		return out, nil
	}
	original := in.req.header
	if in.kind == HTTPResponse {
		original = in.header
	}
	if v := field("response"); v != nil && in.kind == HTTPRequest {
		res, ok := v.(*goja.Object)
		if !ok {
			return out, errors.New("response must be an object")
		}
		l := &local{status: 200, header: http.Header{}}
		if s := res.Get("status"); s != nil && !goja.IsUndefined(s) {
			l.status = int(s.ToInteger())
		}
		if l.status < 200 || l.status > 999 {
			return out, fmt.Errorf("response status %d must be 200 to 999", l.status)
		}
		if h := res.Get("headers"); h != nil && !goja.IsUndefined(h) && !goja.IsNull(h) {
			l.header = r.header(h, nil)
		}
		l.body, _ = r.bytesOf(res.Get("body"))
		out.response = l
		return out, nil
	}
	if v := field("url"); v != nil && in.kind == HTTPRequest {
		out.url = v.String()
	}
	if v := field("status"); v != nil && in.kind == HTTPResponse {
		out.status = int(v.ToInteger())
	}
	if v := field("headers"); v != nil {
		out.header = r.header(v, original)
		if in.kind == HTTPRequest && out.header.Get("Host") == "" && in.req.host != "" {
			out.header.Set("Host", in.req.host)
		}
	}
	if v := field("body"); v != nil {
		out.body, out.hasBody = r.bytesOf(v)
	}
	return out, nil
}

// header reads a header object or a full-header-mode list. A value returned
// as it was presented keeps its original lines, so joined duplicates such as
// Set-Cookie survive a script that only touched other headers.
func (r *run) header(v goja.Value, original http.Header) http.Header {
	h := http.Header{}
	add := func(k string, val goja.Value) {
		if val == nil || goja.IsUndefined(val) || goja.IsNull(val) || framing(k) || !httpguts.ValidHeaderFieldName(k) {
			return
		}
		ck := http.CanonicalHeaderKey(k)
		if list, ok := val.Export().([]any); ok {
			for _, item := range list {
				if s := fmt.Sprint(item); httpguts.ValidHeaderFieldValue(s) {
					h[ck] = append(h[ck], s)
				}
			}
			return
		}
		s := val.String()
		if lines := original[ck]; len(lines) > 1 && strings.Join(lines, ", ") == s {
			h[ck] = append(h[ck], lines...)
			return
		}
		if httpguts.ValidHeaderFieldValue(s) {
			h[ck] = append(h[ck], s)
		}
	}
	o, ok := v.(*goja.Object)
	if !ok {
		return h
	}
	if o.ClassName() == "Array" {
		for _, item := range o.Keys() {
			if line, ok := o.Get(item).(*goja.Object); ok {
				add(line.Get("field").String(), line.Get("value"))
			}
		}
		return h
	}
	for _, k := range o.Keys() {
		add(k, o.Get(k))
	}
	return h
}
