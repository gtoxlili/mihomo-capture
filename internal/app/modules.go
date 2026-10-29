package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxModuleBytes = 2 << 20

func moduleHosts(text string) (string, []string) {
	section := ""
	mode := "append"
	var hosts []string
	s := bufio.NewScanner(strings.NewReader(text))
	s.Buffer(make([]byte, 4096), maxModuleBytes)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line)
			continue
		}
		if section != "[mitm]" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "hostname" {
			continue
		}
		v = strings.TrimSpace(v)
		mode = "replace"
		if strings.HasPrefix(v, "%APPEND% ") {
			mode = "append"
			v = strings.TrimSpace(strings.TrimPrefix(v, "%APPEND%"))
		} else if strings.HasPrefix(v, "%INSERT% ") {
			mode = "insert"
			v = strings.TrimSpace(strings.TrimPrefix(v, "%INSERT%"))
		}
		if i := strings.Index(v, " #"); i >= 0 {
			v = v[:i]
		}
		hosts = nil
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hosts = append(hosts, strings.ToLower(h))
			}
		}
	}
	return mode, hosts
}

func matchHosts(hosts []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	positive := false
	for _, h := range hosts {
		neg := strings.HasPrefix(h, "-")
		h = strings.TrimPrefix(h, "-")
		p := "^" + strings.ReplaceAll(strings.ReplaceAll(regexp.QuoteMeta(h), `\*`, ".*"), `\?`, ".") + "$"
		ok, _ := regexp.MatchString(p, host)
		if ok && neg {
			return false
		}
		positive = positive || ok
	}
	return positive
}

func parseModule(source, text string, args map[string]string) (Module, error) {
	m := Module{ID: newID(), Name: filepath.Base(source), Source: source, Content: text, LoadedAt: time.Now().UTC(), Processing: "hostname-only"}
	// Parameter defaults come from Surge's argument table, then caller overrides.
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#!arguments=") {
			for _, p := range strings.Split(strings.TrimPrefix(line, "#!arguments="), ",") {
				k, v, _ := strings.Cut(p, ":")
				values[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		if strings.HasPrefix(line, "#!name=") {
			m.Name = strings.TrimSpace(strings.TrimPrefix(line, "#!name="))
		}
	}
	for k, v := range args {
		if _, ok := values[k]; !ok {
			return m, fmt.Errorf("undeclared module argument: %s", k)
		}
		values[k] = v
	}
	for k, v := range values {
		text = strings.ReplaceAll(text, "{{{"+k+"}}}", v)
	}
	_, m.Hostnames = moduleHosts(text)
	if len(m.Hostnames) == 0 {
		return m, errors.New("module has no MITM hostname declaration")
	}
	for _, h := range m.Hostnames {
		h = strings.TrimPrefix(h, "-")
		if h == "" || strings.ContainsAny(h, " \t\r\n/,:{}%") {
			return m, fmt.Errorf("unsupported hostname: %q", h)
		}
	}
	// Store the expanded module so restart behavior is independent of remote changes.
	m.Content = text
	sum := sha256.Sum256([]byte(text))
	m.SHA256 = hex.EncodeToString(sum[:])
	return m, nil
}

func (a *App) RegisterModule(ctx context.Context, source string, args map[string]string) (Module, error) {
	var m Module
	if len(source) > 4096 {
		return m, errors.New("module source is too long")
	}
	var r io.ReadCloser
	u, err := url.Parse(source)
	if err != nil {
		return m, err
	}
	if u.Scheme == "https" || u.Scheme == "http" {
		req, err := http.NewRequestWithContext(ctx, "GET", source, nil)
		if err != nil {
			return m, err
		}
		c := http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second}}
		defer c.CloseIdleConnections()
		resp, err := c.Do(req)
		if err != nil {
			return m, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return m, fmt.Errorf("module download returned HTTP %d", resp.StatusCode)
		}
		r = resp.Body
	} else {
		if u.Scheme != "" && u.Scheme != "file" {
			return m, errors.New("module source must be HTTP(S) URL or absolute file path")
		}
		p := source
		if u.Scheme == "file" {
			if u.Host != "" {
				return m, errors.New("file URL must be local")
			}
			p = u.Path
		}
		if !filepath.IsAbs(p) {
			return m, errors.New("local module path must be absolute")
		}
		f, err := os.Open(p)
		if err != nil {
			return m, err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxModuleBytes+1))
	if err != nil {
		return m, err
	}
	if len(b) > maxModuleBytes {
		return m, errors.New("module exceeds 2 MiB")
	}
	m, err = parseModule(source, string(b), args)
	if err != nil {
		return m, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.modules) >= 32 {
		return m, errors.New("maximum 32 modules")
	}
	for _, x := range a.modules {
		if x.Source == source {
			return m, errors.New("source is already registered; remove it before replacing")
		}
	}
	a.modules = append(a.modules, m)
	if err = a.saveLocked(); err != nil {
		a.modules = a.modules[:len(a.modules)-1]
		return m, err
	}
	if err = a.syncLocked(ctx); err != nil {
		a.modules = a.modules[:len(a.modules)-1]
		_ = a.saveLocked()
		return m, err
	}
	return m, nil
}

func (a *App) RemoveModule(ctx context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	old := append([]Module{}, a.modules...)
	found := false
	for i, m := range a.modules {
		if m.ID == id {
			a.modules = append(a.modules[:i], a.modules[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return os.ErrNotExist
	}
	if err := a.saveLocked(); err != nil {
		a.modules = old
		return err
	}
	if err := a.syncLocked(ctx); err != nil {
		a.modules = old
		_ = a.saveLocked()
		return err
	}
	return nil
}
