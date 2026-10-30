package module

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func compile(t *testing.T, d Definition) *Module {
	t.Helper()
	if d.Name == "" {
		d.Name = "fixture"
	}
	if d.Hostnames == nil {
		d.Hostnames = []string{"*"}
	}
	m, err := Compile("m1", d)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func engine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine(Options{StorePath: filepath.Join(t.TempDir(), "store.json")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func exchange(mods ...*Module) *Exchange { return &Exchange{ID: "req-1", Modules: mods} }

func upstream(req *http.Request, status int, header http.Header, body []byte) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}
}

func read(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// contract: docs/api.md — definitions are validated when written, and the
// stored form has every default filled in.
func TestCompileValidatesAndNormalizes(t *testing.T) {
	for name, d := range map[string]Definition{
		"port in hostname":   {Name: "x", Hostnames: []string{"example.com:8443"}},
		"bad pattern":        {Name: "x", URLRewrites: []URLRewrite{{Pattern: "(", Replacement: "x"}}},
		"framing header":     {Name: "x", HeaderRewrites: []HeaderRewrite{{Pattern: ".", Action: "header-del", Name: "Content-Length"}}},
		"unknown action":     {Name: "x", HeaderRewrites: []HeaderRewrite{{Pattern: ".", Action: "header-set", Name: "A"}}},
		"script syntax":      {Name: "x", Scripts: []Script{{Name: "s", Pattern: ".", Code: "$done({"}}},
		"duplicate script":   {Name: "x", Scripts: []Script{{Name: "s", Pattern: ".", Code: "$done()"}, {Name: "s", Pattern: ".", Code: "$done()"}}},
		"bad jq":             {Name: "x", BodyRewrites: []BodyRewrite{{Type: HTTPResponseJQ, Pattern: ".", JQ: ".a |"}}},
		"map local status":   {Name: "x", MapLocals: []MapLocal{{Pattern: ".", StatusCode: 100}}},
		"missing name":       {},
		"oversized max_size": {Name: "x", Scripts: []Script{{Name: "s", Pattern: ".", Code: "$done()", MaxSize: MaxBodySize + 1}}},
	} {
		if _, err := Compile("id", d); err == nil {
			t.Errorf("%s accepted", name)
		} else if _, ok := err.(Invalid); !ok {
			t.Errorf("%s: %T is not a definition error", name, err)
		}
	}
	m, err := Compile("id", Definition{Name: " x ", Hostnames: []string{" *.Example.COM. ", ""}, URLRewrites: []URLRewrite{{Pattern: "^http://a", Type: "reject"}}, Scripts: []Script{{Name: "s", Pattern: ".", Code: "$done()", MaxSize: -1}}})
	if err != nil {
		t.Fatal(err)
	}
	d := m.Definition
	if d.Name != "x" || strings.Join(d.Hostnames, ",") != "*.example.com" || d.URLRewrites[0].Replacement != "_" || d.Scripts[0].Type != HTTPRequest || d.Scripts[0].Timeout != 5 || d.Scripts[0].MaxSize != MaxBodySize || d.HeaderRewrites == nil || d.MapLocals == nil {
		t.Fatalf("normalized definition: %+v", d)
	}
}

// contract: docs/api.md — hostnames take * and ? wildcards, "-" excludes,
// and the first matching entry decides.
func TestHostnamesFirstEntryDecides(t *testing.T) {
	m := compile(t, Definition{Hostnames: []string{"-*.apple.com", "*", "-never.example"}})
	for host, want := range map[string]bool{"www.apple.com": false, "apple.com": true, "never.example": true, "API.Example.com.": true} {
		if got := m.MatchHost(host); got != want {
			t.Errorf("%s: %v", host, got)
		}
	}
	q := compile(t, Definition{Hostnames: []string{"a?.example.com"}})
	if !q.MatchHost("ab.example.com") || q.MatchHost("abc.example.com") || q.MatchHost("x.ab.example.com") {
		t.Fatal("? wildcard")
	}
}

// contract: docs/api.md — replacement templates: $1 takes no more digits than
// the group count has, ${name} names a group, and \ escapes.
func TestReplacementTemplate(t *testing.T) {
	for _, c := range []struct{ re, in, tpl, want string }{
		{`^https://a\.com/(.*)$`, "https://a.com/x", "https://b.com/$1abc", "https://b.com/xabc"},
		{`(a)`, "a", "$10", "a0"},
		{`(?<word>a)`, "a", "[${word}]", "[a]"},
		{`a`, "a", `\$1 $0`, "$1 a"},
		{`(?<=x)a`, "xa", "b$0", "xba"}, // lookbehind takes the backtracking engine
		{`(?<=中)(文)`, "中文字中文", "[$1]", "中[文]字中[文]"},
		{`(a)(b)?`, "a", "[$2]", "[]"},
	} {
		p, err := compilePattern(c.re, false)
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := p.replace(c.in, c.tpl)
		if err != nil || got != c.want {
			t.Errorf("%s on %s with %s: %q %v", c.re, c.in, c.tpl, got, err)
		}
	}
}

// contract: docs/api.md — header mode rewrites in place and updates Host;
// 302/307 answer with Location; reject answers 404; the first matching rule
// decides.
func TestURLRewrite(t *testing.T) {
	e := engine(t)
	m := compile(t, Definition{URLRewrites: []URLRewrite{
		{Pattern: `^https://ads\.example\.com/`, Type: "reject"},
		{Pattern: `^https://old\.example\.com/(.*)`, Replacement: "https://new.example.com/v2/$1", Type: "header"},
		{Pattern: `^https://old\.example\.com/`, Replacement: "https://never.example.com/", Type: "302"},
		{Pattern: `^http://short\.example\.com/(.*)`, Replacement: "https://long.example.com/$1", Type: "307"},
	}})
	r := httptest.NewRequest("GET", "https://old.example.com/a?b=1", nil)
	x := exchange(m)
	if res := e.Request(x, r); res != nil || r.URL.String() != "https://new.example.com/v2/a?b=1" || r.Host != "new.example.com" {
		t.Fatalf("header rewrite: %v %s %s", res, r.URL, r.Host)
	}
	if strings.Join(x.Applied(), ",") != "fixture" {
		t.Fatalf("applied %v", x.Applied())
	}
	res := e.Request(exchange(m), httptest.NewRequest("POST", "http://short.example.com/p", nil))
	if res == nil || res.StatusCode != 307 || res.Header.Get("Location") != "https://long.example.com/p" {
		t.Fatalf("307: %+v", res)
	}
	x = exchange(m)
	if res = e.Request(x, httptest.NewRequest("GET", "https://ads.example.com/x.js", nil)); res == nil || res.StatusCode != 404 {
		t.Fatalf("reject: %+v", res)
	}
	// A locally answered request skips the response phase.
	e.Response(x, res)
}

// contract: docs/api.md — every matching header rewrite applies, in order, in
// both directions.
func TestHeaderRewrite(t *testing.T) {
	e := engine(t)
	m := compile(t, Definition{HeaderRewrites: []HeaderRewrite{
		{Pattern: "example", Action: "header-add", Name: "DNT", Value: "1"},
		{Pattern: "example", Action: "header-del", Name: "Cookie"},
		{Pattern: "example", Action: "header-replace", Name: "X-Absent", Value: "never"},
		{Pattern: "example", Action: "header-replace-regex", Name: "User-Agent", Regex: `Safari/(\d+)`, Replacement: "Chrome/$1"},
		{Pattern: "example", Action: "header-replace", Name: "Host", Value: "origin.example.com"},
		{Type: HTTPResponse, Pattern: "example", Action: "header-replace", Name: "Cache-Control", Value: "no-store"},
	}})
	r := httptest.NewRequest("GET", "https://www.example.com/", nil)
	r.Header.Set("Cookie", "a=1")
	r.Header.Set("User-Agent", "Mozilla Safari/605")
	x := exchange(m)
	e.Request(x, r)
	if r.Header.Get("DNT") != "1" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Absent") != "" || r.Header.Get("User-Agent") != "Mozilla Chrome/605" || r.Host != "origin.example.com" {
		t.Fatalf("request headers: %v host %s", r.Header, r.Host)
	}
	res := upstream(r, 200, http.Header{"Cache-Control": {"max-age=60"}}, nil)
	e.Response(x, res)
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("response headers: %v", res.Header)
	}
}

// contract: docs/api.md — map local answers without the upstream, with the
// documented default Content-Type per data type.
func TestMapLocal(t *testing.T) {
	e := engine(t)
	m := compile(t, Definition{MapLocals: []MapLocal{
		{Pattern: `/json$`, Data: "{}", StatusCode: 500, Headers: map[string]string{"Content-Type": "application/json"}},
		{Pattern: `/gif$`, DataType: "tiny-gif"},
		{Pattern: `/bin$`, DataType: "base64", Data: base64.StdEncoding.EncodeToString([]byte{0, 1})},
	}})
	res := e.Request(exchange(m), httptest.NewRequest("GET", "https://a.example/json", nil))
	if res.StatusCode != 500 || res.Header.Get("Content-Type") != "application/json" || read(t, res.Body) != "{}" {
		t.Fatalf("text: %+v", res)
	}
	res = e.Request(exchange(m), httptest.NewRequest("GET", "https://a.example/gif", nil))
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/gif" || !bytes.Equal([]byte(read(t, res.Body)), tinyGIF) {
		t.Fatal("tiny-gif")
	}
	res = e.Request(exchange(m), httptest.NewRequest("GET", "https://a.example/bin", nil))
	if res.Header.Get("Content-Type") != "application/octet-stream" || read(t, res.Body) != "\x00\x01" {
		t.Fatal("base64")
	}
}

// contract: docs/api.md — pairs apply in order with ^ and $ at each line;
// compressed bodies are decompressed and the length recomputed; jq's empty
// output keeps the body; non-JSON and oversized bodies pass.
func TestBodyRewrite(t *testing.T) {
	e := engine(t)
	m := compile(t, Definition{BodyRewrites: []BodyRewrite{
		{Type: HTTPResponse, Pattern: "/text", Replacements: []Replacement{{Regex: "^ad: .*$", Replacement: "ad: none"}, {Regex: "none", Replacement: "[$0]"}}},
		{Type: HTTPResponseJQ, Pattern: "/json", JQ: `.ads = [] | .id += 0`},
		{Type: HTTPResponseJQ, Pattern: "/empty", JQ: `empty`},
		{Type: HTTPRequest, Pattern: "/post", Replacements: []Replacement{{Regex: "^$", Replacement: "generated"}}},
	}})
	var z bytes.Buffer
	w := gzip.NewWriter(&z)
	_, _ = io.WriteString(w, "keep\nad: banner\nkeep")
	_ = w.Close()
	r := httptest.NewRequest("GET", "https://a.example/text", nil)
	x := exchange(m)
	e.Request(x, r)
	res := upstream(r, 200, http.Header{"Content-Encoding": {"gzip"}}, z.Bytes())
	e.Response(x, res)
	if got := read(t, res.Body); got != "keep\nad: [none]\nkeep" || res.Header.Get("Content-Encoding") != "" || res.ContentLength != int64(len(got)) {
		t.Fatalf("regex rewrite: %q %v", got, res.Header)
	}

	r = httptest.NewRequest("GET", "https://a.example/json", nil)
	x = exchange(m)
	e.Request(x, r)
	res = upstream(r, 200, nil, []byte(`{"id":9007199254740993,"ads":[1]}`))
	e.Response(x, res)
	if got := read(t, res.Body); got != `{"ads":[],"id":9007199254740993}` {
		t.Fatalf("jq: %s", got)
	}
	for _, path := range []string{"/json", "/empty"} {
		r = httptest.NewRequest("GET", "https://a.example"+path, nil)
		x = exchange(m)
		e.Request(x, r)
		res = upstream(r, 200, nil, []byte(`not json`))
		e.Response(x, res)
		if got := read(t, res.Body); got != "not json" {
			t.Fatalf("%s changed a non-JSON body: %q", path, got)
		}
	}
	r = httptest.NewRequest("GET", "https://a.example/json", nil)
	x = exchange(m)
	e.Request(x, r)
	big := append([]byte(`{"ads":"`), append(bytes.Repeat([]byte("x"), RewriteBodySize), '"', '}')...)
	res = upstream(r, 200, nil, big)
	e.Response(x, res)
	if got := read(t, res.Body); got != string(big) {
		t.Fatal("oversized body was not passed through intact")
	}
	if logs := e.Logs("m1"); len(logs) == 0 || !strings.Contains(logs[0].Message, "exceeds") {
		t.Fatalf("oversized body not noted: %+v", logs)
	}

	r = httptest.NewRequest("POST", "https://a.example/post", nil)
	e.Request(exchange(m), r)
	if got := read(t, r.Body); got != "generated" || r.ContentLength != 9 {
		t.Fatalf("request body: %q", got)
	}
}

// contract: docs/api.md — $request, $done semantics for both types, one
// script per message, and failures that leave the message alone.
func TestScripts(t *testing.T) {
	e := engine(t)
	m := compile(t, Definition{Scripts: []Script{
		{Name: "modify", Pattern: "/modify", RequiresBody: true, Argument: "arg", Code: `
			const h = $request.headers;
			h["x-arg"] = $argument + ":" + $request.id;
			delete h["COOKIE"];
			$done({url: $request.url + "?changed=1", headers: h, body: $request.body.toUpperCase()});`},
		{Name: "never", Pattern: "/modify", Code: `$done({abort: true})`},
		{Name: "answer", Pattern: "/answer", Code: `$done({response: {status: 201, headers: {"Content-Type": "text/plain"}, body: "local"}})`},
		{Name: "abort", Pattern: "/abort", Code: `$done({abort: true})`},
		{Name: "throws", Pattern: "/throws", Code: `null.x`},
		{Name: "slow", Pattern: "/slow", Timeout: 1, Code: `while (true) {}`},
		{Name: "silent", Pattern: "/silent", Code: `console.log("no done", {a: 1})`},
		{Name: "async", Pattern: "/async", Code: `
			(async () => {
				await new Promise((ok) => setTimeout(ok, 10));
				const before = $persistentStore.read();
				$persistentStore.write(String(Number(before || 0) + 1));
				$done({headers: {...$request.headers, "X-Count": $persistentStore.read()}});
			})();`},
		{Name: "response", Type: HTTPResponse, Pattern: "/modify", RequiresBody: true, Code: `
			const body = JSON.parse($response.body);
			body.seen = $request.url;
			$done({status: 299, headers: $response.headers, body: JSON.stringify(body)});`},
		{Name: "binary", Type: HTTPResponse, Pattern: "/binary", RequiresBody: true, BinaryBodyMode: true, Code: `
			const b = $response.body; b[0] = 0x41; $done({body: b});`},
	}})
	r := httptest.NewRequest("POST", "https://a.example/modify", strings.NewReader("hello"))
	r.Header.Set("Cookie", "secret")
	x := exchange(m)
	if res := e.Request(x, r); res != nil {
		t.Fatal("only the first matching script may run")
	}
	if r.URL.String() != "https://a.example/modify?changed=1" || r.Host != "a.example" || r.Header.Get("X-Arg") != "arg:req-1" || r.Header.Get("Cookie") != "" || read(t, r.Body) != "HELLO" {
		t.Fatalf("modified request: %s %s %v", r.URL, r.Host, r.Header)
	}
	res := upstream(r, 200, http.Header{"Set-Cookie": {"a=1", "b=2"}, "Content-Type": {"application/json"}}, []byte(`{"ok":true}`))
	e.Response(x, res)
	if got := read(t, res.Body); res.StatusCode != 299 || got != `{"ok":true,"seen":"https://a.example/modify?changed=1"}` {
		t.Fatalf("modified response: %d %s", res.StatusCode, got)
	}
	// Headers returned as presented keep their original lines.
	if strings.Join(res.Header.Values("Set-Cookie"), "|") != "a=1|b=2" {
		t.Fatalf("Set-Cookie lines: %v", res.Header.Values("Set-Cookie"))
	}

	res = e.Request(exchange(m), httptest.NewRequest("GET", "https://a.example/answer", nil))
	if res == nil || res.StatusCode != 201 || read(t, res.Body) != "local" {
		t.Fatalf("local response: %+v", res)
	}
	x = exchange(m)
	if e.Request(x, httptest.NewRequest("GET", "https://a.example/abort", nil)); !x.Abort {
		t.Fatal("abort")
	}
	for _, path := range []string{"/throws", "/slow", "/silent"} {
		r = httptest.NewRequest("GET", "https://a.example"+path, nil)
		r.Header.Set("X-Keep", "1")
		start := time.Now()
		if res := e.Request(exchange(m), r); res != nil || r.Header.Get("X-Keep") != "1" || r.URL.Path != path {
			t.Fatalf("%s changed the request", path)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatalf("%s ran past its timeout", path)
		}
	}
	logs := e.Logs("m1")
	joined := ""
	for _, l := range logs {
		joined += l.Level + " " + l.Source + " " + l.Message + "\n"
	}
	for _, want := range []string{"error throws", "error slow", "timed out", "warn silent", "info silent", `{"a":1}`} {
		if !strings.Contains(joined, want) {
			t.Errorf("log lacks %q:\n%s", want, joined)
		}
	}
	for i := 1; i <= 2; i++ {
		r = httptest.NewRequest("GET", "https://a.example/async", nil)
		e.Request(exchange(m), r)
		if r.Header.Get("X-Count") != string(rune('0'+i)) {
			t.Fatalf("persistent store run %d: %v", i, r.Header)
		}
	}
	r = httptest.NewRequest("GET", "https://a.example/binary", nil)
	x = exchange(m)
	e.Request(x, r)
	res = upstream(r, 200, nil, []byte{0, 0xff})
	e.Response(x, res)
	if got := read(t, res.Body); got != "A\xff" {
		t.Fatalf("binary body: %q", got)
	}
}

// contract: docs/api.md — $httpClient calls back with (error, response,
// data); the store survives a restart.
func TestScriptAPIs(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Method", r.Method)
		_, _ = io.WriteString(w, r.Header.Get("Content-Type")+"|"+string(b))
	}))
	defer api.Close()
	path := filepath.Join(t.TempDir(), "store.json")
	e, err := NewEngine(Options{StorePath: path})
	if err != nil {
		t.Fatal(err)
	}
	m := compile(t, Definition{Scripts: []Script{{Name: "fetch", Pattern: ".", Code: `
		$httpClient.post({url: "` + api.URL + `", body: {a: 1}}, (err, res, data) => {
			if (err) { $done({response: {status: 500, body: err}}); return }
			$httpClient.get("http://127.0.0.1:1/", (err2) => {
				$persistentStore.write("v", "shared");
				$done({response: {status: res.status, headers: {"X-Method": res.headers["X-Method"]}, body: data + "|" + (err2 ? "failed" : "reached")}});
			});
		});`}}})
	res := e.Request(exchange(m), httptest.NewRequest("GET", "https://a.example/", nil))
	if res == nil || res.StatusCode != 200 || res.Header.Get("X-Method") != "POST" || read(t, res.Body) != `application/json|{"a":1}|failed` {
		t.Fatalf("$httpClient: %+v %v", res, e.Logs("m1"))
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := reopened.read(sharedKey("shared")); !ok || v != "v" {
		t.Fatal("store did not survive a restart")
	}
	_ = context.Background
}

// upstream: WHATWG Encoding Standard — TextEncoder and TextDecoder, including
// encodeInto, BOM handling, streaming, replacement, fatal and legacy labels.
func TestTextEncoderDecoder(t *testing.T) {
	e := engine(t)
	m := compile(t, Definition{Scripts: []Script{{Name: "codec", Pattern: ".", Code: `
		const u8 = (...b) => new Uint8Array(b)
		const hex = (a) => Array.from(a, (x) => x.toString(16).padStart(2, "0")).join(" ")
		const threw = (f) => { try { f(); return "none" } catch (err) { return err.constructor.name } }
		const enc = new TextEncoder()
		const dest = new Uint8Array(5)
		const into = enc.encodeInto("a😀b", dest)
		const s = new TextDecoder()
		const r = {
			encoding: enc.encoding,
			encode: hex(enc.encode("é😀")),
			lone: hex(enc.encode("\ud800")),
			into: into.read + "/" + into.written + " " + hex(dest),
			bom: new TextDecoder().decode(u8(0xef, 0xbb, 0xbf, 0x68, 0x69)),
			keepBOM: new TextDecoder("utf-8", { ignoreBOM: true }).decode(u8(0xef, 0xbb, 0xbf, 0x68)).length,
			stream: s.decode(u8(0xe2, 0x82), { stream: true }) + "|" + s.decode(u8(0xac)),
			replaced: new TextDecoder().decode(u8(0x61, 0xe2, 0x82, 0x62)),
			fatal: threw(() => new TextDecoder("utf-8", { fatal: true }).decode(u8(0x61, 0xff))),
			fatalOK: new TextDecoder("utf-8", { fatal: true }).decode(u8(0x61)),
			gbk: new TextDecoder(" GBK ").decode(u8(0xc4, 0xe3, 0xba, 0xc3)),
			utf16: new TextDecoder("utf-16").encoding + " " + new TextDecoder("utf-16").decode(u8(0xff, 0xfe, 0x41, 0x00)),
			unknown: threw(() => new TextDecoder("no-such-encoding")),
			notBuffer: threw(() => new TextDecoder().decode("text")),
			buffers: new TextDecoder().decode(u8(0x68, 0x69).buffer) + new TextDecoder().decode(new DataView(u8(0x78, 0x68, 0x69).buffer, 1)),
			tag: Object.prototype.toString.call(new TextDecoder()) + " " + (new TextEncoder() instanceof TextEncoder),
		}
		$done({ response: { body: JSON.stringify(r) } })`}}})
	res := e.Request(exchange(m), httptest.NewRequest("GET", "https://a.example/", nil))
	if res == nil {
		t.Fatalf("no response: %+v", e.Logs("m1"))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(read(t, res.Body)), &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{
		"encoding":  "utf-8",
		"encode":    "c3 a9 f0 9f 98 80",
		"lone":      "ef bf bd",
		"into":      "3/5 61 f0 9f 98 80",
		"bom":       "hi",
		"keepBOM":   float64(2),
		"stream":    "|€",
		"replaced":  "a�b",
		"fatal":     "TypeError",
		"fatalOK":   "a",
		"gbk":       "你好",
		"utf16":     "utf-16le A",
		"unknown":   "RangeError",
		"notBuffer": "TypeError",
		"buffers":   "hihi",
		"tag":       "[object TextDecoder] true",
	} {
		if got[k] != want {
			t.Errorf("%s: got %#v, want %#v", k, got[k], want)
		}
	}
}
