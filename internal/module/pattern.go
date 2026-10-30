package module

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
)

// pattern is a regular expression. RE2 runs in linear time and takes nearly
// every pattern; lookaround and backreferences fall back to a backtracking
// engine bounded by a match timeout.
type pattern struct {
	re  *regexp.Regexp
	alt *regexp2.Regexp
}

const backtrackTimeout = 500 * time.Millisecond

func compilePattern(src string, multiline bool) (*pattern, error) {
	prefix := ""
	if multiline {
		prefix = "(?m)"
	}
	if re, err := regexp.Compile(prefix + src); err == nil {
		return &pattern{re: re}, nil
	}
	opts := regexp2.None
	if multiline {
		opts = regexp2.Multiline
	}
	alt, err := regexp2.Compile(src, opts)
	if err != nil {
		return nil, err
	}
	alt.MatchTimeout = backtrackTimeout
	return &pattern{alt: alt}, nil
}

// errTimeout is a backtracking match that ran past its time bound; the rule is
// treated as not matching.
var errTimeout = errors.New("regular expression timed out")

func (p *pattern) match(s string) (bool, error) {
	if p.re != nil {
		return p.re.MatchString(s), nil
	}
	ok, err := p.alt.MatchString(s)
	if err != nil {
		return false, errTimeout
	}
	return ok, nil
}

// replace substitutes every match with the template: $n names a capture
// group, taking no more digits than the number of groups has, \ escapes the
// next character, and ${name} names a named group.
func (p *pattern) replace(s, template string) (string, bool, error) {
	var b strings.Builder
	last, n := 0, 0
	if p.re != nil {
		names := p.re.SubexpNames()
		for _, m := range p.re.FindAllStringSubmatchIndex(s, -1) {
			b.WriteString(s[last:m[0]])
			expand(&b, template, len(m)/2-1, func(i int) string {
				if m[2*i] < 0 {
					return ""
				}
				return s[m[2*i]:m[2*i+1]]
			}, func(name string) int {
				for i, v := range names {
					if v == name && v != "" {
						return i
					}
				}
				return -1
			})
			last = m[1]
			n++
		}
		if n == 0 {
			return s, false, nil
		}
		b.WriteString(s[last:])
		return b.String(), true, nil
	}
	m, err := p.alt.FindStringMatch(s)
	for ; m != nil && err == nil; m, err = p.alt.FindNextMatch(m) {
		start, length := m.ByteRange()
		b.WriteString(s[last:start])
		groups := m.Groups()
		expand(&b, template, len(groups)-1, func(i int) string {
			if len(groups[i].Captures) == 0 {
				return ""
			}
			return groups[i].String()
		}, func(name string) int {
			for i := range groups {
				if groups[i].Name == name {
					return i
				}
			}
			return -1
		})
		last = start + length
		n++
	}
	if err != nil {
		return s, false, errTimeout
	}
	if n == 0 {
		return s, false, nil
	}
	b.WriteString(s[last:])
	return b.String(), true, nil
}

func expand(b *strings.Builder, template string, groups int, group func(int) string, named func(string) int) {
	digits := len(strconv.Itoa(groups))
	for i := 0; i < len(template); i++ {
		c := template[i]
		switch {
		case c == '\\' && i+1 < len(template):
			i++
			b.WriteByte(template[i])
		case c == '$' && i+1 < len(template) && template[i+1] >= '0' && template[i+1] <= '9':
			j, n := i+1, 0
			for j < len(template) && j-i-1 < digits && template[j] >= '0' && template[j] <= '9' {
				n = n*10 + int(template[j]-'0')
				j++
			}
			if n <= groups {
				b.WriteString(group(n))
			}
			i = j - 1
		case c == '$' && i+1 < len(template) && template[i+1] == '{':
			end := strings.IndexByte(template[i:], '}')
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			if g := named(template[i+2 : i+end]); g >= 0 {
				b.WriteString(group(g))
			}
			i += end
		default:
			b.WriteByte(c)
		}
	}
}

// hostEntry is one entry of a module's hostname list.
type hostEntry struct {
	glob    string
	exclude bool
}

func (e hostEntry) String() string {
	if e.exclude {
		return "-" + e.glob
	}
	return e.glob
}

func parseHost(raw string) (hostEntry, error) {
	h := strings.ToLower(strings.TrimSpace(raw))
	e := hostEntry{}
	if strings.HasPrefix(h, "-") {
		e.exclude = true
		h = strings.TrimSpace(h[1:])
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		if e.exclude {
			return e, errors.New("empty exclusion")
		}
		return e, nil
	}
	if len(h) > 253 {
		return e, errors.New("hostname exceeds 253 characters")
	}
	if strings.Contains(h, ":") {
		return e, fmt.Errorf("%q: ports are not part of a hostname here; an entry covers every port", raw)
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("-._*?", r)) {
			return e, fmt.Errorf("%q: invalid character %q", raw, r)
		}
	}
	e.glob = h
	return e, nil
}

// matchHosts applies the list in order: the first entry that matches decides.
func matchHosts(entries []hostEntry, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, e := range entries {
		if glob(e.glob, host) {
			return !e.exclude
		}
	}
	return false
}

// glob matches * as any run of characters and ? as one character, the
// wildcards of module hostnames and of mihomo's DOMAIN-WILDCARD.
func glob(p, s string) bool {
	px, sx := 0, 0
	star, mark := -1, 0
	for sx < len(s) {
		switch {
		case px < len(p) && (p[px] == '?' || p[px] == s[sx]):
			px++
			sx++
		case px < len(p) && p[px] == '*':
			star, mark = px, sx
			px++
		case star >= 0:
			px = star + 1
			mark++
			sx = mark
		default:
			return false
		}
	}
	for px < len(p) && p[px] == '*' {
		px++
	}
	return px == len(p)
}
