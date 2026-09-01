package metrics

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeProc builds a fake /proc so parsing is checked against known values
// rather than whatever the host running the tests happens to report.
func writeProc(t *testing.T, stat, meminfo, loadavg, uptime string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range map[string]string{
		"stat":    stat,
		"meminfo": meminfo,
		"loadavg": loadavg,
		"uptime":  uptime,
	} {
		if content == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

const (
	// user nice system idle iowait irq softirq steal
	statA = "cpu  100 0 50 800 50 0 0 0\ncpu0 1 2 3 4 5 6 7 8\n"
	// +100 busy (user), +100 idle. Expect 50%.
	statB = "cpu  200 0 50 900 50 0 0 0\ncpu0 1 2 3 4 5 6 7 8\n"

	meminfo = "MemTotal:       8000000 kB\nMemFree:        1000000 kB\nMemAvailable:   6000000 kB\nBuffers:          10000 kB\n"
	loadavg = "0.52 0.31 0.24 2/512 9911\n"
	uptime  = "123456.78 987654.32\n"
)

func TestCollectParsesProc(t *testing.T) {
	proc := writeProc(t, statA, meminfo, loadavg, uptime)
	c := NewCollector(proc, "")

	// First sample primes the CPU delta and must report 0, not a bogus value
	// derived from boot-time cumulative counters.
	first := c.Collect(time.Now(), 0, 0)
	if first.CPUPercent != 0 {
		t.Errorf("first CPUPercent = %v, want 0", first.CPUPercent)
	}

	if first.MemTotal != 8_000_000*1024 {
		t.Errorf("MemTotal = %d", first.MemTotal)
	}
	// used = total - available = 8000000 - 6000000 kB
	if first.MemUsed != 2_000_000*1024 {
		t.Errorf("MemUsed = %d, want total-available", first.MemUsed)
	}
	if first.MemPercent != 25 {
		t.Errorf("MemPercent = %v, want 25", first.MemPercent)
	}
	if first.Load1 != 0.52 || first.Load5 != 0.31 || first.Load15 != 0.24 {
		t.Errorf("load = %v %v %v", first.Load1, first.Load5, first.Load15)
	}
	if first.Uptime != 123456 {
		t.Errorf("Uptime = %d, want 123456", first.Uptime)
	}
	if first.DiskTotal != 0 {
		t.Error("disk should be omitted when no path is configured")
	}
}

func TestCPUPercentFromDelta(t *testing.T) {
	proc := writeProc(t, statA, meminfo, loadavg, uptime)
	c := NewCollector(proc, "")

	c.Collect(time.Now(), 0, 0)

	if err := os.WriteFile(filepath.Join(proc, "stat"), []byte(statB), 0o644); err != nil {
		t.Fatal(err)
	}

	second := c.Collect(time.Now(), 0, 0)
	if second.CPUPercent != 50 {
		t.Errorf("CPUPercent = %v, want 50", second.CPUPercent)
	}
}

// A missing loadavg must not blank the rest of the panel.
func TestCollectToleratesMissingFiles(t *testing.T) {
	proc := writeProc(t, statA, meminfo, "", "")
	c := NewCollector(proc, "")

	s := c.Collect(time.Now(), 7, 1.5)
	if s.MemTotal == 0 {
		t.Error("memory should still be read when loadavg is absent")
	}
	if s.Load1 != 0 || s.Uptime != 0 {
		t.Error("absent readings should be zero, not garbage")
	}
	if s.RequestsTotal != 7 || s.RequestsPerSec != 1.5 {
		t.Errorf("request stats not carried through: %+v", s)
	}
	if s.Goroutines == 0 {
		t.Error("goroutine count should always be available")
	}
}

func TestCollectorHandlesAbsentProc(t *testing.T) {
	c := NewCollector(filepath.Join(t.TempDir(), "nope"), "")

	s := c.Collect(time.Now(), 0, 0)
	if s.MemTotal != 0 || s.CPUPercent != 0 {
		t.Errorf("expected zeroed host fields, got %+v", s)
	}
	if s.Goroutines == 0 {
		t.Error("process fields should still be populated")
	}
}

func TestDiskUsageWhenConfigured(t *testing.T) {
	proc := writeProc(t, statA, meminfo, loadavg, uptime)
	s := NewCollector(proc, t.TempDir()).Collect(time.Now(), 0, 0)

	if s.DiskTotal == 0 {
		t.Error("DiskTotal should be populated for a real path")
	}
	if s.DiskUsed > s.DiskTotal {
		t.Errorf("used %d exceeds total %d", s.DiskUsed, s.DiskTotal)
	}
}

func TestCounterMiddleware(t *testing.T) {
	var counter Counter
	h := counter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	for i := 0; i < 3; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}

	if counter.Total() != 3 {
		t.Errorf("Total = %d, want 3", counter.Total())
	}
}

func newTestHub(t *testing.T, maxSubs int) *Hub {
	t.Helper()
	proc := writeProc(t, statA, meminfo, loadavg, uptime)
	return NewHub(NewCollector(proc, ""), &Counter{}, 10*time.Millisecond, maxSubs)
}

func TestHubBroadcastsToSubscribers(t *testing.T) {
	hub := newTestHub(t, 4)

	ch, release, ok := hub.Subscribe()
	if !ok {
		t.Fatal("Subscribe refused")
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	select {
	case s := <-ch:
		if s.MemTotal == 0 {
			t.Errorf("broadcast snapshot looks empty: %+v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no snapshot broadcast")
	}
}

func TestHubEnforcesCapacity(t *testing.T) {
	hub := newTestHub(t, 1)

	_, release, ok := hub.Subscribe()
	if !ok {
		t.Fatal("first Subscribe refused")
	}

	if _, _, ok := hub.Subscribe(); ok {
		t.Error("second Subscribe should be refused at capacity")
	}

	// Releasing must free the slot.
	release()
	if _, _, ok := hub.Subscribe(); !ok {
		t.Error("Subscribe refused after release")
	}
}

// A subscriber that never reads must not stall the sampler for everyone else.
func TestHubDoesNotBlockOnSlowSubscriber(t *testing.T) {
	hub := newTestHub(t, 4)

	_, releaseSlow, _ := hub.Subscribe() // deliberately never drained
	defer releaseSlow()

	fast, releaseFast, _ := hub.Subscribe()
	defer releaseFast()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	received := 0
	deadline := time.After(2 * time.Second)
	for received < 3 {
		select {
		case <-fast:
			received++
		case <-deadline:
			t.Fatalf("fast subscriber got %d snapshots; slow one blocked the sampler", received)
		}
	}
}

func TestStreamEmitsSSE(t *testing.T) {
	hub := newTestHub(t, 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	// Wait for the hub to have something to send.
	for i := 0; i < 100; i++ {
		if _, ok := hub.Latest(); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	server := httptest.NewServer(Stream(hub))
	defer server.Close()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer reqCancel()

	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, server.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		var s Snapshot
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &s); err != nil {
			t.Fatalf("event payload is not valid JSON: %v", err)
		}
		if s.MemTotal == 0 {
			t.Errorf("streamed snapshot looks empty: %+v", s)
		}
		return
	}
	t.Fatal("no data frame received")
}

func TestStreamRefusesWhenFull(t *testing.T) {
	hub := newTestHub(t, 1)

	_, release, _ := hub.Subscribe()
	defer release()

	rec := httptest.NewRecorder()
	Stream(hub).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics/stream", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("503 should carry Retry-After")
	}
}

// The first /proc/stat read has nothing to diff against, so it must not be
// published as a confident 0%.
func TestHubDoesNotPublishPrimingSample(t *testing.T) {
	proc := writeProc(t, statA, meminfo, loadavg, uptime)
	hub := NewHub(NewCollector(proc, ""), &Counter{}, time.Hour, 4)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	// The interval is an hour, so anything present now came from priming.
	time.Sleep(50 * time.Millisecond)

	if _, ok := hub.Latest(); ok {
		t.Error("priming sample was published; a connecting client would see CPU 0%")
	}
}
