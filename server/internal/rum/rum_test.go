package rum

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func at(t time.Time) func() time.Time { return func() time.Time { return t } }

// A histogram is lossy by design, so assert the estimate lands within one
// bucket width rather than demanding exactness.
func TestQuantileAccuracy(t *testing.T) {
	h := NewHistogram(MetricLCP)

	// 1..1000ms, uniform. True p75 is 750.
	for v := 1; v <= 1000; v++ {
		h.Add(float64(v))
	}

	got := h.Quantile(0.75)
	if math.Abs(got-750) > 750*0.15 {
		t.Errorf("p75 = %.0f, want within 15%% of 750", got)
	}

	if h.Total() != 1000 {
		t.Errorf("Total = %d, want 1000", h.Total())
	}
}

func TestQuantileEmpty(t *testing.T) {
	if got := NewHistogram(MetricLCP).Quantile(0.75); got != 0 {
		t.Errorf("empty p75 = %v, want 0", got)
	}
}

func TestCLSUsesItsOwnScale(t *testing.T) {
	h := NewHistogram(MetricCLS)
	for i := 0; i < 100; i++ {
		h.Add(0.05)
	}

	got := h.Quantile(0.75)
	if got <= 0 || got > 0.1 {
		t.Errorf("CLS p75 = %v, want between 0 and 0.1", got)
	}
	if r := Rating(MetricCLS, got); r != "good" {
		t.Errorf("rating = %q, want good", r)
	}
}

func TestRatingThresholds(t *testing.T) {
	tests := []struct {
		metric string
		value  float64
		want   string
	}{
		{MetricLCP, 2000, "good"},
		{MetricLCP, 2500, "good"},
		{MetricLCP, 3000, "needs-improvement"},
		{MetricLCP, 5000, "poor"},
		{MetricINP, 150, "good"},
		{MetricCLS, 0.05, "good"},
		{MetricCLS, 0.3, "poor"},
	}

	for _, tc := range tests {
		if got := Rating(tc.metric, tc.value); got != tc.want {
			t.Errorf("Rating(%s, %v) = %q, want %q", tc.metric, tc.value, got, tc.want)
		}
	}
}

func TestFractionBelow(t *testing.T) {
	h := NewHistogram(MetricLCP)
	for i := 0; i < 75; i++ {
		h.Add(500) // good
	}
	for i := 0; i < 25; i++ {
		h.Add(6000) // poor
	}

	got := h.FractionBelow(2500)
	if math.Abs(got-0.75) > 0.02 {
		t.Errorf("FractionBelow(2500) = %.3f, want ~0.75", got)
	}
}

func TestAddRejectsGarbage(t *testing.T) {
	h := NewHistogram(MetricLCP)
	h.Add(math.NaN())
	h.Add(-1)

	if h.Total() != 0 {
		t.Errorf("Total = %d, want 0", h.Total())
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rum.json")

	s, err := NewStore(path, 28)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for i := 0; i < 10; i++ {
		s.Add(MetricLCP, "IN", 900, fixedNow)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := NewStore(path, 28)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	report := reloaded.Report(fixedNow)
	if report.Samples != 10 {
		t.Errorf("Samples = %d, want 10", report.Samples)
	}
	if len(report.Metrics) != 1 || report.Metrics[0].Metric != MetricLCP {
		t.Fatalf("Metrics = %+v", report.Metrics)
	}
	if report.Countries[0].Country != "IN" {
		t.Errorf("Country = %q, want IN", report.Countries[0].Country)
	}
}

func TestMissingCheckpointIsNotAnError(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "absent.json"), 28)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s.Report(fixedNow).Samples != 0 {
		t.Error("fresh store should be empty")
	}
}

func TestPruneDropsStaleDaysOnly(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "rum.json"), 28)

	s.Add(MetricLCP, "IN", 900, fixedNow.AddDate(0, 0, -40)) // outside window
	s.Add(MetricLCP, "IN", 900, fixedNow)                    // inside

	if got := s.Report(fixedNow).Samples; got != 1 {
		t.Errorf("report before prune counted %d, want 1 (stale day must be excluded)", got)
	}

	if removed := s.Prune(fixedNow); removed != 1 {
		t.Errorf("Prune removed %d, want 1", removed)
	}
	if got := s.Report(fixedNow).Samples; got != 1 {
		t.Errorf("after prune Samples = %d, want 1", got)
	}
}

func post(t *testing.T, h http.Handler, body string, headers map[string]string) *http.Response {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/rum", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestIngestAcceptsValidBeacon(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "rum.json"), 28)
	h := Ingest(s, at(fixedNow))

	resp := post(t, h, `{"metrics":[{"name":"LCP","value":812},{"name":"CLS","value":0.02}]}`,
		map[string]string{"CF-IPCountry": "in"})

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	report := s.Report(fixedNow)
	if report.Samples != 2 {
		t.Errorf("Samples = %d, want 2", report.Samples)
	}
	if report.Countries[0].Country != "IN" {
		t.Errorf("country = %q, want IN (uppercased)", report.Countries[0].Country)
	}
}

func TestIngestRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"malformed json", `not json`, http.StatusBadRequest},
		{"unknown metric", `{"metrics":[{"name":"HACK","value":1}]}`, http.StatusBadRequest},
		{"negative value", `{"metrics":[{"name":"LCP","value":-5}]}`, http.StatusBadRequest},
		{"absurd timing", `{"metrics":[{"name":"LCP","value":999999}]}`, http.StatusBadRequest},
		{"empty metrics", `{"metrics":[]}`, http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := NewStore(filepath.Join(t.TempDir(), "rum.json"), 28)
			resp := post(t, Ingest(s, at(fixedNow)), tc.body, nil)

			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if n := s.Report(fixedNow).Samples; n != 0 {
				t.Errorf("stored %d samples, want 0", n)
			}
		})
	}
}

func TestIngestCapsBodySize(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "rum.json"), 28)
	huge := `{"metrics":[` + strings.Repeat(`{"name":"LCP","value":1},`, 500) + `{"name":"LCP","value":1}]}`

	resp := post(t, Ingest(s, at(fixedNow)), huge, nil)
	if resp.StatusCode == http.StatusNoContent {
		t.Error("oversized body was accepted")
	}
}

func TestIngestRateLimits(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "rum.json"), 28)
	h := Ingest(s, at(fixedNow))
	body := `{"metrics":[{"name":"LCP","value":800}]}`
	hdr := map[string]string{"CF-Connecting-IP": "203.0.113.9"}

	for i := 0; i < rateLimit; i++ {
		if resp := post(t, h, body, hdr); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("request %d: status = %d, want 204", i, resp.StatusCode)
		}
	}

	if resp := post(t, h, body, hdr); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status after limit = %d, want 429", resp.StatusCode)
	}

	// A different address must be unaffected by the first one's budget.
	other := post(t, h, body, map[string]string{"CF-Connecting-IP": "198.51.100.4"})
	if other.StatusCode != http.StatusNoContent {
		t.Errorf("second IP status = %d, want 204", other.StatusCode)
	}
}

func TestServeReturnsJSON(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "rum.json"), 28)
	s.Add(MetricLCP, "DE", 1200, fixedNow)

	req := httptest.NewRequest(http.MethodGet, "/api/perf", nil)
	rec := httptest.NewRecorder()
	Serve(s, at(fixedNow)).ServeHTTP(rec, req)

	resp := rec.Result()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}

	var report Report
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if report.WindowDays != 28 || report.Samples != 1 {
		t.Errorf("report = %+v", report)
	}
}
