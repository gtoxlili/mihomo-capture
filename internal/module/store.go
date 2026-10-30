package module

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// $persistentStore limits: a value as on iOS, and the whole store.
const (
	maxStoreValue = 4 << 20
	maxStoreTotal = 32 << 20
	flushDelay    = time.Second
)

// Store is $persistentStore. Writes are collected for a second before the
// file is replaced, so scripts that write on every request cost one write.
type Store struct {
	mu    sync.Mutex
	path  string
	data  map[string]string
	size  int
	timer *time.Timer
	err   error
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, data: map[string]string{}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &s.data); err != nil {
		return nil, errors.New("invalid script store: " + err.Error())
	}
	for k, v := range s.data {
		s.size += len(k) + len(v)
	}
	return s, nil
}

// scriptKey is where a script's keyless value lives, shared by the runs of
// one script; an explicit key is shared by every script.
func scriptKey(module, name string) string { return "script/" + module + "/" + name }

func sharedKey(key string) string { return "key/" + key }

func (s *Store) read(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok
}

// write stores a value, or deletes the entry for nil; false means it did not
// fit.
func (s *Store) write(key string, value *string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, had := s.data[key]
	size := s.size
	if had {
		size -= len(key) + len(old)
	}
	if value == nil {
		if had {
			delete(s.data, key)
			s.size = size
			s.schedule()
		}
		return true
	}
	if len(*value) > maxStoreValue || size+len(key)+len(*value) > maxStoreTotal {
		return false
	}
	s.data[key] = *value
	s.size = size + len(key) + len(*value)
	s.schedule()
	return true
}

func (s *Store) deletePrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.data {
		if strings.HasPrefix(k, prefix) {
			delete(s.data, k)
			s.size -= len(k) + len(v)
			s.schedule()
		}
	}
}

func (s *Store) schedule() {
	if s.path == "" || s.timer != nil {
		return
	}
	s.timer = time.AfterFunc(flushDelay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.flushLocked()
	})
}

func (s *Store) flushLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.path == "" {
		return
	}
	b, err := json.Marshal(s.data)
	if err == nil {
		err = writeFile(s.path, b)
	}
	s.err = err
}

// Close writes pending changes.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.flushLocked()
	}
	return s.err
}

func writeFile(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
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
