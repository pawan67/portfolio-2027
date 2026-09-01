package rum

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const dayFormat = "2006-01-02"

// bucketKey identifies one histogram: a metric, on a day, from a country.
// Keeping the day dimension is what makes the rolling window prunable; keeping
// country lets the report break down by region without storing any visitor
// identity.
type bucketKey struct {
	Metric  string `json:"metric"`
	Day     string `json:"day"`
	Country string `json:"country"`
}

type record struct {
	bucketKey
	Counts []int `json:"counts"`
}

// Store holds aggregated field data in memory and checkpoints it to a JSON
// file. There are no per-visitor rows: an observation is folded into a counter
// and the original value is discarded.
type Store struct {
	mu         sync.RWMutex
	data       map[bucketKey]*Histogram
	dirty      bool
	path       string
	windowDays int
}

// NewStore loads any existing checkpoint from path. A missing file is not an
// error -- it is simply the first run.
func NewStore(path string, windowDays int) (*Store, error) {
	s := &Store{
		data:       map[bucketKey]*Histogram{},
		path:       path,
		windowDays: windowDays,
	}

	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}

	var records []record
	if err := json.Unmarshal(b, &records); err != nil {
		return nil, err
	}

	for _, r := range records {
		s.data[r.bucketKey] = &Histogram{Metric: r.Metric, Counts: r.Counts}
	}
	return s, nil
}

// Add folds one observation into the appropriate histogram.
func (s *Store) Add(metric, country string, value float64, now time.Time) {
	if country == "" {
		country = "??"
	}

	k := bucketKey{
		Metric:  metric,
		Day:     now.UTC().Format(dayFormat),
		Country: country,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.data[k]
	if !ok {
		h = NewHistogram(metric)
		s.data[k] = h
	}
	h.Add(value)
	s.dirty = true
}

// Prune drops histograms that have fallen out of the rolling window.
func (s *Store) Prune(now time.Time) int {
	cutoff := now.UTC().AddDate(0, 0, -s.windowDays).Format(dayFormat)

	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for k := range s.data {
		if k.Day < cutoff {
			delete(s.data, k)
			removed++
		}
	}
	if removed > 0 {
		s.dirty = true
	}
	return removed
}

// Save writes a checkpoint if anything changed since the last one. The write is
// atomic: a temp file in the same directory, then a rename.
func (s *Store) Save() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}

	records := make([]record, 0, len(s.data))
	for k, h := range s.data {
		records = append(records, record{bucketKey: k, Counts: h.Counts})
	}
	s.dirty = false
	s.mu.Unlock()

	// Stable order keeps the checkpoint diffable and the write deterministic.
	sort.Slice(records, func(i, j int) bool {
		if records[i].Day != records[j].Day {
			return records[i].Day < records[j].Day
		}
		if records[i].Metric != records[j].Metric {
			return records[i].Metric < records[j].Metric
		}
		return records[i].Country < records[j].Country
	})

	b, err := json.Marshal(records)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
