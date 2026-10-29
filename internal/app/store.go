package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// recordMeta keeps each record's summary in memory, so listing reads a file
// only to search its content.
type recordMeta struct {
	RecordSummary
	Size int64 // bytes of the record file
}
type Store struct {
	mu        sync.Mutex
	dir       string
	c         Config
	index     map[string]recordMeta
	bytes     int64
	lastError string
}

var recordID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func NewStore(dir string, c Config) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, c: c, index: map[string]recordMeta{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".pending-") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !recordID.MatchString(id) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var r Record
		if json.Unmarshal(b, &r) != nil || r.ID != id {
			return nil, errors.New("invalid persisted record: " + e.Name())
		}
		s.index[id] = recordMeta{r.summary(), int64(len(b))}
		s.bytes += int64(len(b))
	}
	if err = s.pruneLocked(0, 0); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) sortedLocked() []recordMeta {
	r := make([]recordMeta, 0, len(s.index))
	for _, m := range s.index {
		r = append(r, m)
	}
	sort.Slice(r, func(i, j int) bool {
		if r[i].At.Equal(r[j].At) {
			return r[i].ID < r[j].ID
		}
		return r[i].At.Before(r[j].At)
	})
	return r
}
func (s *Store) removeLocked(id string) error {
	m, ok := s.index[id]
	if !ok {
		return nil
	}
	if err := os.Remove(filepath.Join(s.dir, id+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(s.index, id)
	s.bytes -= m.Size
	return nil
}
func (s *Store) pruneLocked(extra int64, count int) error {
	cut := time.Now().Add(-time.Duration(s.c.RetentionHours) * time.Hour)
	for _, m := range s.sortedLocked() {
		if m.At.Before(cut) || s.bytes+extra > s.c.MaxDiskBytes || len(s.index)+count > s.c.MaxRecords {
			if err := s.removeLocked(m.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) Put(r *Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if int64(len(b)) > s.c.MaxDiskBytes {
		return errors.New("record exceeds storage budget")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !recordID.MatchString(r.ID) {
		return errors.New("invalid record ID")
	}
	// Removing the old version before writing reserves room for the temporary file.
	if err = s.removeLocked(r.ID); err != nil {
		return err
	}
	if err = s.pruneLocked(int64(len(b)), 1); err != nil {
		return err
	}
	if err = atomicJSON(filepath.Join(s.dir, r.ID+".json"), r); err != nil {
		return err
	}
	s.index[r.ID] = recordMeta{r.summary(), int64(len(b))}
	s.bytes += int64(len(b))
	return nil
}
func (s *Store) noteError(err error) { s.mu.Lock(); defer s.mu.Unlock(); s.lastError = err.Error() }
func (s *Store) Get(id string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}
func (s *Store) getLocked(id string) (*Record, error) {
	if _, ok := s.index[id]; !ok {
		return nil, os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(s.dir, id+".json"))
	if err != nil {
		return nil, err
	}
	var r Record
	err = json.Unmarshal(b, &r)
	return &r, err
}
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.index {
		if err := s.removeLocked(id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Prune() error { s.mu.Lock(); defer s.mu.Unlock(); return s.pruneLocked(0, 0) }

type Filter struct {
	Host, Path, Method, Keyword, Device string
	Status, Limit                       int
	Before                              string
}

func (s *Store) List(f Filter) ([]RecordSummary, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	out := []RecordSummary{}
	keyword := bytes.ToLower([]byte(f.Keyword))
	all := s.sortedLocked()
	past := f.Before == ""
	next := ""
	for i := len(all) - 1; i >= 0; i-- {
		m := all[i]
		if !past {
			if m.ID == f.Before {
				past = true
			}
			continue
		}
		if f.Host != "" && !strings.Contains(strings.ToLower(m.Host), strings.ToLower(f.Host)) || f.Path != "" && !strings.Contains(m.Path, f.Path) || f.Method != "" && m.Method != strings.ToUpper(f.Method) || f.Status != 0 && m.Status != f.Status || f.Device != "" && m.DeviceID != f.Device {
			continue
		}
		if f.Keyword != "" {
			// The file is the record's JSON, which is what the keyword searches.
			b, err := os.ReadFile(filepath.Join(s.dir, m.ID+".json"))
			if err != nil {
				return nil, "", err
			}
			if !bytes.Contains(bytes.ToLower(b), keyword) {
				continue
			}
		}
		if len(out) == f.Limit {
			next = out[len(out)-1].ID
			break
		}
		// Lists return summaries; detail and frame payloads are fetched by ID.
		out = append(out, m.RecordSummary)
	}
	return out, next, nil
}
