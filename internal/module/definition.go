// Package module is the service's HTTP processing. A module names the hosts it
// decrypts and the rewrites and scripts that act on their requests. It is a
// structured form written through the API; docs/api.md is its specification.
package module

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/itchyny/gojq"
	"golang.org/x/net/http/httpguts"
)

// Rule directions.
const (
	HTTPRequest    = "http-request"
	HTTPResponse   = "http-response"
	HTTPRequestJQ  = "http-request-jq"
	HTTPResponseJQ = "http-response-jq"
)

// Limits of one module definition.
const (
	MaxHostnames   = 256
	MaxRules       = 1024 // per rule list
	MaxScripts     = 64
	MaxScriptBytes = 1 << 20
	// MaxBodySize is the largest body a script's max_size admits, and the size
	// -1 stands for.
	MaxBodySize = 32 << 20
	// RewriteBodySize is the body rewrite limit and the default max_size.
	// Larger bodies pass through unmodified.
	RewriteBodySize = 10 << 20
	maxPattern      = 4096
	maxTimeout      = 60
)

type Definition struct {
	Name string `json:"name"`
	// Hostnames are the hosts the module decrypts and acts on: * and ?
	// wildcards, a leading - excludes, the first matching entry decides. An
	// entry covers every port of the host.
	Hostnames      []string        `json:"hostnames"`
	HeaderRewrites []HeaderRewrite `json:"header_rewrites"`
	URLRewrites    []URLRewrite    `json:"url_rewrites"`
	MapLocals      []MapLocal      `json:"map_locals"`
	BodyRewrites   []BodyRewrite   `json:"body_rewrites"`
	Scripts        []Script        `json:"scripts"`
}

type HeaderRewrite struct {
	Type    string `json:"type"` // http-request or http-response
	Pattern string `json:"pattern"`
	// Action is header-add, header-del, header-replace or header-replace-regex.
	Action string `json:"action"`
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"` // header-add and header-replace
	// Regex and Replacement are header-replace-regex's pair.
	Regex       string `json:"regex,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

type URLRewrite struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Type        string `json:"type"` // header (default), 302, 307 or reject
}

type MapLocal struct {
	Pattern    string            `json:"pattern"`
	DataType   string            `json:"data_type"` // text (default), base64 or tiny-gif
	Data       string            `json:"data"`
	StatusCode int               `json:"status_code"` // 200..999, default 200
	Headers    map[string]string `json:"headers,omitempty"`
}

type BodyRewrite struct {
	Type    string `json:"type"` // http-request, http-response, http-request-jq or http-response-jq
	Pattern string `json:"pattern"`
	// Replacements apply in order to the regex types; JQ is the jq types' filter.
	Replacements []Replacement `json:"replacements,omitempty"`
	JQ           string        `json:"jq,omitempty"`
}

type Replacement struct {
	Regex       string `json:"regex"`
	Replacement string `json:"replacement"`
}

type Script struct {
	Name           string `json:"name"`
	Type           string `json:"type"` // http-request or http-response
	Pattern        string `json:"pattern"`
	Code           string `json:"code"`
	RequiresBody   bool   `json:"requires_body"`
	BinaryBodyMode bool   `json:"binary_body_mode"`
	FullHeaderMode bool   `json:"full_header_mode"`
	MaxSize        int64  `json:"max_size"` // bytes, default 10 MiB; -1 means MaxBodySize
	Timeout        int    `json:"timeout"`  // seconds, default 5
	Argument       string `json:"argument,omitempty"`
}

// Invalid is a definition error, reported to the API caller as a bad request.
type Invalid struct{ error }

func invalid(format string, args ...any) error { return Invalid{fmt.Errorf(format, args...)} }

// Module is a compiled definition. It is immutable and shared by every
// request it acts on.
type Module struct {
	ID         string
	Definition Definition // normalized: defaults filled in, empty lists present
	hosts      []hostEntry
	headers    [2][]headerRule // by direction
	urls       []urlRule
	locals     []localRule
	bodies     [2][]bodyRule
	scripts    [2][]*script
}

const (
	request = iota
	response
)

type headerRule struct {
	pattern     *pattern
	action      string
	name        string
	value       string
	regex       *pattern
	replacement string
}

type urlRule struct {
	pattern     *pattern
	replacement string
	kind        string
}

type localRule struct {
	pattern *pattern
	status  int
	header  http.Header
	body    []byte
}

type bodyRule struct {
	pattern *pattern
	pairs   []bodyPair
	jq      *gojq.Code
}

type bodyPair struct {
	regex       *pattern
	replacement string
}

type script struct {
	Script
	pattern  *pattern
	program  *goja.Program
	timeout  time.Duration
	maxBytes int64
}

// tinyGIF is the 1×1 transparent GIF Map Local's tiny-gif returns.
var tinyGIF, _ = base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")

// Compile validates a definition and prepares it for requests.
func Compile(id string, d Definition) (*Module, error) {
	m := &Module{ID: id}
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" || len(d.Name) > 100 {
		return nil, invalid("name must be 1 to 100 characters")
	}
	if len(d.Hostnames) > MaxHostnames {
		return nil, invalid("at most %d hostnames", MaxHostnames)
	}
	hostnames := []string{}
	for i, h := range d.Hostnames {
		e, err := parseHost(h)
		if err != nil {
			return nil, invalid("hostnames[%d]: %v", i, err)
		}
		if e.glob == "" {
			continue
		}
		m.hosts = append(m.hosts, e)
		hostnames = append(hostnames, e.String())
	}
	d.Hostnames = hostnames
	for _, n := range []int{len(d.HeaderRewrites), len(d.URLRewrites), len(d.MapLocals), len(d.BodyRewrites)} {
		if n > MaxRules {
			return nil, invalid("at most %d rules in each list", MaxRules)
		}
	}
	if len(d.Scripts) > MaxScripts {
		return nil, invalid("at most %d scripts", MaxScripts)
	}
	if d.HeaderRewrites == nil {
		d.HeaderRewrites = []HeaderRewrite{}
	}
	for i := range d.HeaderRewrites {
		if err := m.addHeader(&d.HeaderRewrites[i]); err != nil {
			return nil, invalid("header_rewrites[%d]: %v", i, err)
		}
	}
	if d.URLRewrites == nil {
		d.URLRewrites = []URLRewrite{}
	}
	for i := range d.URLRewrites {
		if err := m.addURL(&d.URLRewrites[i]); err != nil {
			return nil, invalid("url_rewrites[%d]: %v", i, err)
		}
	}
	if d.MapLocals == nil {
		d.MapLocals = []MapLocal{}
	}
	for i := range d.MapLocals {
		if err := m.addLocal(&d.MapLocals[i]); err != nil {
			return nil, invalid("map_locals[%d]: %v", i, err)
		}
	}
	if d.BodyRewrites == nil {
		d.BodyRewrites = []BodyRewrite{}
	}
	for i := range d.BodyRewrites {
		if err := m.addBody(&d.BodyRewrites[i]); err != nil {
			return nil, invalid("body_rewrites[%d]: %v", i, err)
		}
	}
	if d.Scripts == nil {
		d.Scripts = []Script{}
	}
	names := map[string]bool{}
	for i := range d.Scripts {
		if err := m.addScript(&d.Scripts[i]); err != nil {
			return nil, invalid("scripts[%d]: %v", i, err)
		}
		if names[d.Scripts[i].Name] {
			return nil, invalid("scripts[%d]: name %q is used twice", i, d.Scripts[i].Name)
		}
		names[d.Scripts[i].Name] = true
	}
	m.Definition = d
	return m, nil
}

func (m *Module) Name() string { return m.Definition.Name }

// MatchHost reports whether the module acts on a host.
func (m *Module) MatchHost(host string) bool { return matchHosts(m.hosts, host) }

func direction(t string) (int, error) {
	switch t {
	case "", HTTPRequest:
		return request, nil
	case HTTPResponse:
		return response, nil
	}
	return 0, fmt.Errorf("type must be %s or %s", HTTPRequest, HTTPResponse)
}

func urlPattern(src string) (*pattern, error) {
	if src == "" {
		return nil, errors.New("pattern is required")
	}
	if len(src) > maxPattern {
		return nil, fmt.Errorf("pattern exceeds %d characters", maxPattern)
	}
	return compilePattern(src, false)
}

// framing headers are computed by the service from the body it sends.
func framing(name string) bool {
	return strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "Transfer-Encoding")
}

func (m *Module) addHeader(h *HeaderRewrite) error {
	dir, err := direction(h.Type)
	if err != nil {
		return err
	}
	h.Type = []string{HTTPRequest, HTTPResponse}[dir]
	r := headerRule{action: h.Action, name: http.CanonicalHeaderKey(h.Name)}
	if r.pattern, err = urlPattern(h.Pattern); err != nil {
		return err
	}
	if !httpguts.ValidHeaderFieldName(h.Name) {
		return fmt.Errorf("invalid header name %q", h.Name)
	}
	if framing(h.Name) {
		return errors.New("header rewrites cannot change message framing (Content-Length, Transfer-Encoding)")
	}
	switch h.Action {
	case "header-add", "header-replace":
		if !httpguts.ValidHeaderFieldValue(h.Value) {
			return errors.New("invalid header value")
		}
		r.value = h.Value
		h.Regex, h.Replacement = "", ""
	case "header-del":
		h.Value, h.Regex, h.Replacement = "", "", ""
	case "header-replace-regex":
		if h.Regex == "" {
			return errors.New("header-replace-regex requires regex")
		}
		if r.regex, err = compilePattern(h.Regex, false); err != nil {
			return fmt.Errorf("regex: %w", err)
		}
		if !httpguts.ValidHeaderFieldValue(h.Replacement) {
			return errors.New("invalid replacement")
		}
		r.replacement = h.Replacement
		h.Value = ""
	default:
		return errors.New("action must be header-add, header-del, header-replace or header-replace-regex")
	}
	m.headers[dir] = append(m.headers[dir], r)
	return nil
}

func (m *Module) addURL(u *URLRewrite) error {
	if u.Type == "" {
		u.Type = "header"
	}
	r := urlRule{kind: u.Type, replacement: u.Replacement}
	var err error
	if r.pattern, err = urlPattern(u.Pattern); err != nil {
		return err
	}
	switch u.Type {
	case "header", "302", "307":
		if u.Replacement == "" {
			return errors.New("replacement is required")
		}
	case "reject":
		u.Replacement = "_"
	default:
		return errors.New("type must be header, 302, 307 or reject")
	}
	m.urls = append(m.urls, r)
	return nil
}

func (m *Module) addLocal(l *MapLocal) error {
	if l.DataType == "" {
		l.DataType = "text"
	}
	if l.StatusCode == 0 {
		l.StatusCode = 200
	}
	r := localRule{status: l.StatusCode, header: http.Header{}}
	var err error
	if r.pattern, err = urlPattern(l.Pattern); err != nil {
		return err
	}
	if l.StatusCode < 200 || l.StatusCode > 999 {
		return errors.New("status_code must be 200 to 999")
	}
	switch l.DataType {
	case "text":
		r.body = []byte(l.Data)
		r.header.Set("Content-Type", "text/plain")
	case "base64":
		if r.body, err = base64.StdEncoding.DecodeString(l.Data); err != nil {
			return errors.New("data is not valid base64")
		}
		r.header.Set("Content-Type", "application/octet-stream")
	case "tiny-gif":
		l.Data = ""
		r.body = tinyGIF
		r.header.Set("Content-Type", "image/gif")
	default:
		return errors.New("data_type must be text, base64 or tiny-gif")
	}
	for k, v := range l.Headers {
		if !httpguts.ValidHeaderFieldName(k) || !httpguts.ValidHeaderFieldValue(v) {
			return fmt.Errorf("invalid header %q", k)
		}
		if framing(k) {
			return errors.New("headers cannot set message framing (Content-Length, Transfer-Encoding)")
		}
		r.header.Set(k, v)
	}
	m.locals = append(m.locals, r)
	return nil
}

func (m *Module) addBody(b *BodyRewrite) error {
	r := bodyRule{}
	var err error
	if r.pattern, err = urlPattern(b.Pattern); err != nil {
		return err
	}
	var dir int
	switch b.Type {
	case HTTPRequest, HTTPResponse:
		dir, _ = direction(b.Type)
		if len(b.Replacements) == 0 {
			return errors.New("replacements are required")
		}
		for i, p := range b.Replacements {
			if p.Regex == "" {
				return fmt.Errorf("replacements[%d]: regex is required", i)
			}
			// ^ and $ match at each line of the body.
			re, err := compilePattern(p.Regex, true)
			if err != nil {
				return fmt.Errorf("replacements[%d]: %w", i, err)
			}
			r.pairs = append(r.pairs, bodyPair{re, p.Replacement})
		}
		b.JQ = ""
	case HTTPRequestJQ, HTTPResponseJQ:
		dir, _ = direction(strings.TrimSuffix(b.Type, "-jq"))
		q, err := gojq.Parse(b.JQ)
		if err != nil {
			return fmt.Errorf("jq: %w", err)
		}
		// The filter sees only the body: no environment variables, no inputs.
		if r.jq, err = gojq.Compile(q, gojq.WithEnvironLoader(func() []string { return nil })); err != nil {
			return fmt.Errorf("jq: %w", err)
		}
		b.Replacements = nil
	default:
		return errors.New("type must be http-request, http-response, http-request-jq or http-response-jq")
	}
	m.bodies[dir] = append(m.bodies[dir], r)
	return nil
}

func (m *Module) addScript(s *Script) error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" || len(s.Name) > 100 {
		return errors.New("name must be 1 to 100 characters")
	}
	dir, err := direction(s.Type)
	if err != nil {
		return err
	}
	s.Type = []string{HTTPRequest, HTTPResponse}[dir]
	if s.MaxSize == 0 {
		s.MaxSize = RewriteBodySize
	}
	if s.MaxSize == -1 {
		s.MaxSize = MaxBodySize
	}
	if s.MaxSize < 1 || s.MaxSize > MaxBodySize {
		return fmt.Errorf("max_size must be 1 to %d bytes, or -1", MaxBodySize)
	}
	if s.Timeout == 0 {
		s.Timeout = 5
	}
	if s.Timeout < 1 || s.Timeout > maxTimeout {
		return fmt.Errorf("timeout must be 1 to %d seconds", maxTimeout)
	}
	if len(s.Argument) > 64<<10 {
		return errors.New("argument exceeds 64 KiB")
	}
	if len(s.Code) > MaxScriptBytes {
		return errors.New("code exceeds 1 MiB")
	}
	r := &script{Script: *s, timeout: time.Duration(s.Timeout) * time.Second, maxBytes: s.MaxSize}
	if r.pattern, err = urlPattern(s.Pattern); err != nil {
		return err
	}
	if strings.TrimSpace(s.Code) == "" {
		return errors.New("code is required")
	}
	if r.program, err = goja.Compile(s.Name, s.Code, false); err != nil {
		return fmt.Errorf("code: %w", err)
	}
	m.scripts[dir] = append(m.scripts[dir], r)
	return nil
}
