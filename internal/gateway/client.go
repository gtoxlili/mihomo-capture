// Package gateway routes selected traffic through the capture service with
// stock mihomo: HTTP proxy nodes, file rule providers and one loopback
// listener. The gateway's own config file is never written.
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Selection is the complete set of sources that enter the capture service.
type Selection struct {
	DailyIPs      []string // trusted devices, limited to Hostnames
	Hostnames     []string // Surge MITM hostname patch; a leading "-" excludes
	CaptureIPs    []string // the selected device, every TCP connection
	BlockUDPPorts []uint16 // dropped for the selected device so QUIC falls back to TCP
}

func (s Selection) rules() (map[string][]string, error) {
	out := map[string][]string{LaneCapture: {}, LaneDaily: {}}
	daily, err := sources(s.DailyIPs)
	if err != nil {
		return nil, err
	}
	capture, err := sources(s.CaptureIPs)
	if err != nil {
		return nil, err
	}
	var include, exclude []string
	for _, h := range s.Hostnames {
		negative := strings.HasPrefix(h, "-")
		h = strings.TrimPrefix(h, "-")
		if h == "" || len(h) > 253 || strings.ContainsAny(h, " \t\r\n,()/\\") {
			return nil, fmt.Errorf("invalid hostname pattern %q", h)
		}
		if negative {
			exclude = append(exclude, "(DOMAIN-WILDCARD,"+h+")")
		} else {
			include = append(include, "(DOMAIN-WILDCARD,"+h+")")
		}
	}
	if daily != "" && len(include) > 0 {
		conditions := []string{daily, "(NETWORK,TCP)", anyOf(include)}
		if len(exclude) > 0 {
			conditions = append(conditions, "(NOT,("+anyOf(exclude)+"))")
		}
		out[LaneDaily] = append(out[LaneDaily], "AND,("+strings.Join(conditions, ",")+")")
	}
	if capture != "" {
		out[LaneCapture] = append(out[LaneCapture], "AND,("+capture+",(NETWORK,TCP))")
		var ports []string
		for _, p := range s.BlockUDPPorts {
			if p == 0 {
				return nil, errors.New("UDP ports must be 1..65535")
			}
			ports = append(ports, "(DST-PORT,"+strconv.Itoa(int(p))+")")
		}
		if len(ports) > 0 {
			out[LaneCapture] = append(out[LaneCapture], "AND,("+capture+",(NETWORK,UDP),"+anyOf(ports)+")")
		}
	}
	return out, nil
}

func sources(ips []string) (string, error) {
	var conditions []string
	for _, raw := range ips {
		ip := net.ParseIP(raw)
		if ip == nil {
			return "", fmt.Errorf("invalid device IP %q", raw)
		}
		bits := "/128"
		if ip.To4() != nil {
			bits = "/32"
		}
		conditions = append(conditions, "(SRC-IP-CIDR,"+ip.String()+bits+")")
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return anyOf(conditions), nil
}

func anyOf(conditions []string) string {
	if len(conditions) == 1 {
		return conditions[0]
	}
	return "(OR,(" + strings.Join(conditions, ",") + "))"
}

type Status struct {
	Version string `json:"version"`
	// Catalog reports whether the running gateway has the capture lanes.
	// Without them every connection follows the original rules.
	Catalog bool           `json:"catalog"`
	Rules   map[string]int `json:"rules"`
}

type Client struct {
	ConfigPath      string // the file mihomo runs; read, never written
	StateDir        string
	ProxyAddress    string
	EgressAddress   string
	ControllerURL   string
	ControllerToken string
	Token           string
	http            *http.Client
	mu              sync.Mutex
	installed       bool
}

func NewClient(socketPath, configPath, stateDir string) *Client {
	return &Client{ConfigPath: configPath, StateDir: stateDir, http: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}},
	}}
}

type statusError struct {
	code    int
	message string
}

func (e *statusError) Error() string { return fmt.Sprintf("mihomo HTTP %d: %s", e.code, e.message) }

func notFound(err error) bool {
	var s *statusError
	return errors.As(err, &s) && s.code == http.StatusNotFound
}

func (c *Client) call(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	baseURL := "http://mihomo"
	if c.ControllerURL != "" {
		baseURL = strings.TrimRight(c.ControllerURL, "/")
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.ControllerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.ControllerToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("mihomo controller: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &statusError{resp.StatusCode, strings.TrimSpace(string(b))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// lanes returns the loaded selector rule counts, empty without the catalog.
func (c *Client) lanes(ctx context.Context) (map[string]int, error) {
	var list struct {
		Providers map[string]struct {
			RuleCount int `json:"ruleCount"`
		} `json:"providers"`
	}
	if err := c.call(ctx, "GET", "/providers/rules", nil, &list); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, lane := range lanes {
		if p, ok := list.Providers[laneName(lane)]; ok {
			out[lane] = p.RuleCount
		}
	}
	return out, nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	var version struct {
		Version string `json:"version"`
	}
	if err := c.call(ctx, "GET", "/version", nil, &version); err != nil {
		return s, err
	}
	s.Version = version.Version
	rules, err := c.lanes(ctx)
	if err != nil {
		return s, err
	}
	s.Catalog = len(rules) == len(lanes)
	s.Rules = rules
	return s, nil
}

func (c *Client) options() catalogOptions {
	return catalogOptions{ProxyAddress: c.ProxyAddress, EgressAddress: c.EgressAddress, Token: c.Token, StateDir: c.StateDir}
}

// Check compiles the catalog against the current gateway config without
// loading it.
func (c *Client) Check() error {
	base, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		return err
	}
	_, err = compile(base, c.options())
	return err
}

func (c *Client) write(rules map[string][]string) ([]string, error) {
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return nil, err
	}
	var changed []string
	for _, lane := range lanes {
		path := filepath.Join(c.StateDir, lane+".yaml")
		want, err := yaml.Marshal(map[string][]string{"payload": rules[lane]})
		if err != nil {
			return nil, err
		}
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, want) {
			continue
		}
		if err = atomicWrite(path, want); err != nil {
			return nil, err
		}
		changed = append(changed, lane)
	}
	return changed, nil
}

// refresh makes the running gateway reread selector files. Without the
// catalog there is nothing to reread, which is reported as false.
func (c *Client) refresh(ctx context.Context, changed []string) (bool, error) {
	for _, lane := range changed {
		if err := c.call(ctx, "PUT", "/providers/rules/"+url.PathEscape(laneName(lane)), nil, nil); err != nil {
			if notFound(err) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}

// Select publishes a selection. Changing it rewrites only the selector files;
// the full configuration is loaded once per process, and again whenever the
// gateway has reloaded its own file since.
func (c *Client) Select(ctx context.Context, s Selection) error {
	rules, err := s.rules()
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	changed, err := c.write(rules)
	if err != nil {
		return err
	}
	if c.installed {
		loaded, err := c.lanes(ctx)
		if err != nil {
			return err
		}
		c.installed = len(loaded) == len(lanes)
	}
	if !c.installed {
		if len(rules[LaneCapture])+len(rules[LaneDaily]) == 0 {
			// A catalog from an earlier process may still be loaded.
			_, err = c.refresh(ctx, changed)
			return err
		}
		return c.install(ctx)
	}
	loaded, err := c.refresh(ctx, changed)
	if err == nil && !loaded {
		c.installed = false
		err = c.install(ctx)
	}
	return err
}

func (c *Client) install(ctx context.Context) error {
	base, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		return err
	}
	payload, err := compile(base, c.options())
	if err != nil {
		return err
	}
	// Without force, mihomo keeps its inbound ports and TUN and patches the
	// listeners section, which adds the egress listener.
	if err = c.call(ctx, "PUT", "/configs", map[string]string{"payload": string(payload)}, nil); err != nil {
		return err
	}
	c.installed = true
	return nil
}

// Clear empties both lanes. It needs neither the CA nor the proxy token, so the
// LaunchAgent wrapper runs it whenever the service process ends.
func (c *Client) Clear(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.write(nil); err != nil {
		return err
	}
	_, err := c.refresh(ctx, lanes)
	return err
}

// Remove reloads the gateway's own config file, which has no catalog. A
// gateway without the catalog is left alone.
func (c *Client) Remove(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	loaded, err := c.lanes(ctx)
	if err != nil {
		return err
	}
	if len(loaded) > 0 {
		if err = c.call(ctx, "PUT", "/configs", map[string]string{}, nil); err != nil {
			return err
		}
	}
	c.installed = false
	for _, lane := range lanes {
		if err := os.Remove(filepath.Join(c.StateDir, lane+".yaml")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// UseHTTP switches from the Unix socket transport to a TCP controller.
func (c *Client) UseHTTP(endpoint, token string) error {
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("controller_url must be an http(s) origin")
	}
	c.http.CloseIdleConnections()
	c.http = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	c.ControllerURL, c.ControllerToken = endpoint, token
	return nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

// DialContext leaves through the policy mihomo chose for the original
// connection, identified by the policy ID the proxy node sent.
func (c *Client) DialContext(ctx context.Context, policy, target string) (net.Conn, error) {
	if host, port, err := net.SplitHostPort(target); err != nil || host == "" || port == "" {
		return nil, errors.New("target must be host:port")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.EgressAddress)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	if d, exists := ctx.Deadline(); exists && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	credential := base64.StdEncoding.EncodeToString([]byte(policy + ":" + c.Token))
	req := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{"Proxy-Authorization": {"Basic " + credential}}}
	if err = req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mihomo egress CONNECT: %s", resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	ok = true
	if reader.Buffered() > 0 {
		return &bufferedConn{conn, reader}, nil
	}
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
func (c *bufferedConn) CloseWrite() error {
	if v, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return v.CloseWrite()
	}
	return errors.New("connection cannot half-close writes")
}

func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}
