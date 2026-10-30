package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Lanes lead into the capture service. The capture lane carries the selected
// device; each daily lane carries the trusted devices that share one set of
// active modules, limited to those modules' hostnames.
const (
	LaneHeader   = "X-Capture-Lane"
	PolicyHeader = "X-Capture-Policy"
	LaneCapture  = "capture"
	// ProxyUser authenticates mihomo proxy nodes to the capture service.
	ProxyUser = "capture"
	// MaxDailyLanes bounds the distinct module sets active at once.
	MaxDailyLanes = 64
	minDailyLanes = 2
	dailyPrefix   = "daily-"
)

const prefix = "mihomo-capture-"

// laneNames lists a catalog's lanes in rule order. Capture precedes the daily
// lanes, so the selected device never takes one.
func laneNames(daily int) []string {
	out := []string{LaneCapture}
	for i := range daily {
		out = append(out, dailyLane(i))
	}
	return out
}

func dailyLane(slot int) string { return dailyPrefix + strconv.Itoa(slot) }

// DailySlot reads the slot of a daily lane named in the lane header.
func DailySlot(lane string) (int, bool) {
	n, ok := strings.CutPrefix(lane, dailyPrefix)
	if !ok || n == "" || len(n) > 2 || n[0] == '0' && n != "0" {
		return 0, false
	}
	slot, err := strconv.Atoi(n)
	return slot, err == nil && slot >= 0 && slot < MaxDailyLanes
}

func isLane(lane string) bool {
	_, daily := DailySlot(lane)
	return lane == LaneCapture || daily
}

func laneName(lane string) string { return prefix + lane }

const egressName = prefix + "egress"

// PolicyID is the identifier of a policy in the catalog's proxy nodes and
// egress users.
func PolicyID(policy string) string { return shortID(policy) }

func shortID(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:6])
}

type catalogOptions struct {
	ProxyAddress  string // capture service listener the proxy nodes connect to
	EgressAddress string // mihomo listener the service dials back through
	Token         string
	StateDir      string // selector files; mihomo only reads paths under its home
	Daily         int    // number of daily lanes
}

// compile returns the original configuration plus the capture catalog. The
// original rules stay first-class: each lane is a copy of them, evaluated with
// the device's own metadata, whose policies are replaced by HTTP proxy nodes
// that tell the service which policy mihomo chose. The service then leaves
// through one loopback listener whose users map back to those policies.
func compile(base []byte, o catalogOptions) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(base, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("mihomo config must be a mapping")
	}
	root := doc.Content[0]
	if mode := field(root, "mode"); mode != nil && !strings.EqualFold(mode.Value, "rule") {
		return nil, errors.New("capture lanes require mihomo rule mode")
	}
	proxyHost, proxyPort, err := loopback(o.ProxyAddress)
	if err != nil {
		return nil, fmt.Errorf("capture proxy address: %w", err)
	}
	egressHost, egressPort, err := loopback(o.EgressAddress)
	if err != nil {
		return nil, fmt.Errorf("egress address: %w", err)
	}
	if len(o.Token) < 32 || strings.ContainsAny(o.Token, ":\r\n") {
		return nil, errors.New("proxy token requires at least 32 characters without ':'")
	}
	if !filepath.IsAbs(o.StateDir) {
		return nil, errors.New("gateway state directory must be absolute")
	}
	for _, key := range []string{"port", "socks-port", "mixed-port", "redir-port", "tproxy-port"} {
		if v := field(root, key); v != nil && v.Value == strconv.Itoa(egressPort) {
			return nil, fmt.Errorf("egress port %d is already the gateway %s", egressPort, key)
		}
	}

	known := map[string]bool{"DIRECT": true}
	groups := map[string]*yaml.Node{}
	for _, key := range []string{"proxies", "proxy-groups", "listeners"} {
		for _, item := range items(root, key) {
			name := field(item, "name")
			if name == nil {
				continue
			}
			if strings.HasPrefix(name.Value, prefix) {
				return nil, fmt.Errorf("%s already contains %q", key, name.Value)
			}
			if key == "listeners" {
				if port := field(item, "port"); port != nil && port.Value == strconv.Itoa(egressPort) {
					return nil, fmt.Errorf("egress port %d is already used by listener %q", egressPort, name.Value)
				}
				continue
			}
			known[name.Value] = true
			if key == "proxy-groups" {
				groups[name.Value] = item
				// Such a group would take the lane nodes as ordinary members and
				// send normal traffic, and the egress hop, back into the service.
				for _, all := range []string{"include-all", "include-all-proxies"} {
					if v := field(item, all); v != nil && v.Value == "true" {
						return nil, fmt.Errorf("group %q sets %s, which would include the capture nodes; list its members explicitly", name.Value, all)
					}
				}
			}
		}
	}
	original := map[string]*yaml.Node{}
	if subs := field(root, "sub-rules"); subs != nil {
		for i := 0; i+1 < len(subs.Content); i += 2 {
			original[subs.Content[i].Value] = subs.Content[i+1]
		}
	}
	if providers := field(root, "rule-providers"); providers != nil {
		for i := 0; i < len(providers.Content); i += 2 {
			if strings.HasPrefix(providers.Content[i].Value, prefix) {
				return nil, fmt.Errorf("rule-providers already contains %q", providers.Content[i].Value)
			}
		}
	}
	for name := range original {
		if strings.HasPrefix(name, prefix) {
			return nil, fmt.Errorf("sub-rules already contains %q", name)
		}
	}
	rules := field(root, "rules")
	if rules == nil || rules.Kind != yaml.SequenceNode || len(rules.Content) == 0 {
		return nil, errors.New("mihomo rules are required")
	}
	subNames := make([]string, 0, len(original))
	for name := range original {
		subNames = append(subNames, name)
	}
	sort.Strings(subNames)

	used := map[string]bool{}
	for _, set := range append([]*yaml.Node{rules}, valuesOf(original, subNames)...) {
		for _, rule := range set.Content {
			parts, idx, err := ruleParts(rule.Value)
			if err != nil {
				return nil, err
			}
			target := parts[idx]
			switch {
			case parts[0] == "SUB-RULE":
				if original[target] == nil {
					return nil, fmt.Errorf("unknown sub-rule %q", target)
				}
			case rejected(target):
			case target == "PASS" || target == "PASS-RULE":
				// Inside a lane, PASS would leave the copy instead of continuing it.
				return nil, fmt.Errorf("capture lanes cannot preserve %s in rule %q", target, rule.Value)
			case !known[target]:
				return nil, fmt.Errorf("unknown policy %q in rule %q", target, rule.Value)
			default:
				used[target] = true
			}
		}
	}
	if err = checkGroups(used, groups); err != nil {
		return nil, err
	}
	policies := make([]string, 0, len(used))
	for name := range used {
		policies = append(policies, name)
	}
	sort.Strings(policies)

	proxies := sequence(root, "proxies")
	listeners := sequence(root, "listeners")
	providers := mapping(root, "rule-providers")
	subs := mapping(root, "sub-rules")
	var entries []*yaml.Node
	for _, lane := range laneNames(o.Daily) {
		node := func(policy string) string { return prefix + lane + "-" + shortID(policy) }
		copied := func(sub string) string { return prefix + lane + "-sub-" + shortID(sub) }
		for _, policy := range policies {
			proxies.Content = append(proxies.Content, encode(map[string]any{
				"name": node(policy), "type": "http", "server": proxyHost, "port": proxyPort,
				"username": ProxyUser, "password": o.Token,
				"headers": map[string]string{LaneHeader: lane, PolicyHeader: shortID(policy)},
			}))
		}
		rewrite := func(set *yaml.Node) *yaml.Node {
			out := &yaml.Node{Kind: yaml.SequenceNode}
			for _, rule := range set.Content {
				parts, idx, _ := ruleParts(rule.Value)
				if parts[0] == "SUB-RULE" {
					parts[idx] = copied(parts[idx])
				} else if !rejected(parts[idx]) {
					parts[idx] = node(parts[idx])
				}
				out.Content = append(out.Content, encode(strings.Join(parts, ",")))
			}
			return out
		}
		main := rewrite(rules)
		if lane == LaneCapture {
			// UDP enters the capture lane only for ports chosen to be blocked.
			main.Content = append([]*yaml.Node{encode("NETWORK,UDP,REJECT")}, main.Content...)
		}
		put(subs, laneName(lane), main)
		for _, name := range subNames {
			put(subs, copied(name), rewrite(original[name]))
		}
		put(providers, laneName(lane), encode(map[string]any{
			"type": "file", "behavior": "classical", "format": "yaml",
			"path": filepath.Join(o.StateDir, lane+".yaml"),
		}))
		entries = append(entries, encode("SUB-RULE,(RULE-SET,"+laneName(lane)+"),"+laneName(lane)))
	}
	var users []map[string]string
	egress := &yaml.Node{Kind: yaml.SequenceNode}
	for _, policy := range policies {
		users = append(users, map[string]string{"username": shortID(policy), "password": o.Token})
		egress.Content = append(egress.Content, encode("IN-USER,"+shortID(policy)+","+policy))
	}
	egress.Content = append(egress.Content, encode("MATCH,REJECT"))
	put(subs, egressName, egress)
	listeners.Content = append(listeners.Content, encode(map[string]any{
		"name": egressName, "type": "http", "listen": egressHost, "port": egressPort,
		"users": users, "rule": egressName,
	}))
	rules.Content = append(entries, rules.Content...)
	return yaml.Marshal(&doc)
}

// checkGroups rejects groups whose member choice depends on the source
// address: the service re-enters from loopback and would get another member.
func checkGroups(used map[string]bool, groups map[string]*yaml.Node) error {
	seen := map[string]bool{}
	var walk func(string) error
	walk = func(name string) error {
		g := groups[name]
		if g == nil || seen[name] {
			return nil
		}
		seen[name] = true
		kind := ""
		if t := field(g, "type"); t != nil {
			kind = t.Value
		}
		if kind != "select" && kind != "fallback" && kind != "url-test" {
			return fmt.Errorf("capture cannot reproduce %s group %q from loopback", kind, name)
		}
		if members := field(g, "proxies"); members != nil {
			for _, m := range members.Content {
				if err := walk(m.Value); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for name := range used {
		if err := walk(name); err != nil {
			return err
		}
	}
	return nil
}

func loopback(address string) (string, int, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || n < 1 || n > 65535 {
		return "", 0, errors.New("a loopback IP and port are required")
	}
	return ip.String(), n, nil
}

func rejected(policy string) bool {
	return policy == "REJECT" || policy == "REJECT-DROP"
}

// ruleParts splits a rule at top-level commas and returns the index of its
// policy, skipping trailing parameters such as no-resolve.
func ruleParts(rule string) ([]string, int, error) {
	var parts []string
	depth, start := 0, 0
	for i, r := range rule {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(rule[start:i]))
				start = i + 1
			}
		}
		if depth < 0 {
			return nil, 0, fmt.Errorf("invalid rule: %s", rule)
		}
	}
	parts = append(parts, strings.TrimSpace(rule[start:]))
	if depth != 0 || len(parts) < 2 {
		return nil, 0, fmt.Errorf("invalid rule: %s", rule)
	}
	idx := len(parts) - 1
	for idx > 1 && (parts[idx] == "no-resolve" || parts[idx] == "src") {
		idx--
	}
	return parts, idx, nil
}

func encode(v any) *yaml.Node {
	var n yaml.Node
	_ = n.Encode(v)
	return &n
}

func field(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func items(n *yaml.Node, key string) []*yaml.Node {
	if v := field(n, key); v != nil && v.Kind == yaml.SequenceNode {
		return v.Content
	}
	return nil
}

func put(n *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = value
			return
		}
	}
	n.Content = append(n.Content, encode(key), value)
}

func mapping(n *yaml.Node, key string) *yaml.Node {
	v := field(n, key)
	if v == nil || v.Kind != yaml.MappingNode {
		v = &yaml.Node{Kind: yaml.MappingNode}
		put(n, key, v)
	}
	return v
}

func sequence(n *yaml.Node, key string) *yaml.Node {
	v := field(n, key)
	if v == nil || v.Kind != yaml.SequenceNode {
		v = &yaml.Node{Kind: yaml.SequenceNode}
		put(n, key, v)
	}
	return v
}

func valuesOf(m map[string]*yaml.Node, keys []string) []*yaml.Node {
	out := make([]*yaml.Node, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}
