// Package metrics samples host and process state and streams it to the browser.
//
// Beacon uses gopsutil for this. Here the numbers are read straight out of
// /proc instead: the target is a Linux VPS, the values are the same, and it
// keeps the server free of dependencies. Docker does not namespace /proc, so
// inside the container these are the host machine's figures -- which is exactly
// what "my VPS right now" should mean.
package metrics

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Snapshot is one sample. Field names match Beacon's system widget payload so
// the two are interchangeable.
type Snapshot struct {
	At time.Time `json:"at"`

	CPUPercent float64 `json:"cpuPercent"`
	MemUsed    uint64  `json:"memUsed"`
	MemTotal   uint64  `json:"memTotal"`
	MemPercent float64 `json:"memPercent"`
	Load1      float64 `json:"load1"`
	Load5      float64 `json:"load5"`
	Load15     float64 `json:"load15"`
	Uptime     uint64  `json:"uptime"`

	DiskUsed    uint64  `json:"diskUsed,omitempty"`
	DiskTotal   uint64  `json:"diskTotal,omitempty"`
	DiskPercent float64 `json:"diskPercent,omitempty"`

	// The serving process itself, which the host figures cannot show.
	Goroutines     int     `json:"goroutines"`
	HeapBytes      uint64  `json:"heapBytes"`
	ProcUptime     float64 `json:"procUptime"`
	RequestsTotal  uint64  `json:"requestsTotal"`
	RequestsPerSec float64 `json:"requestsPerSec"`
}

// Collector reads /proc. procRoot is injectable so tests can run against
// fixtures rather than the machine they happen to be on.
type Collector struct {
	procRoot string
	diskPath string
	started  time.Time

	mu       sync.Mutex
	lastBusy uint64
	lastIdle uint64
	hasPrev  bool
}

// NewCollector returns a collector. diskPath may be empty, in which case disk
// usage is omitted rather than reported for a meaningless overlay filesystem.
func NewCollector(procRoot, diskPath string) *Collector {
	if procRoot == "" {
		procRoot = "/proc"
	}
	return &Collector{procRoot: procRoot, diskPath: diskPath, started: time.Now()}
}

// Collect takes a sample. Individual readings that fail are left at zero rather
// than failing the whole snapshot -- a missing loadavg should not blank the
// panel.
func (c *Collector) Collect(now time.Time, requests uint64, perSec float64) Snapshot {
	s := Snapshot{
		At:             now.UTC(),
		Goroutines:     runtime.NumGoroutine(),
		ProcUptime:     now.Sub(c.started).Seconds(),
		RequestsTotal:  requests,
		RequestsPerSec: perSec,
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s.HeapBytes = ms.HeapAlloc

	s.CPUPercent = c.cpuPercent()

	if total, available, ok := c.memory(); ok {
		s.MemTotal = total
		s.MemUsed = total - available
		if total > 0 {
			s.MemPercent = round1(float64(s.MemUsed) / float64(total) * 100)
		}
	}

	if l1, l5, l15, ok := c.loadAvg(); ok {
		s.Load1, s.Load5, s.Load15 = l1, l5, l15
	}

	if up, ok := c.uptime(); ok {
		s.Uptime = up
	}

	if c.diskPath != "" {
		if used, total, ok := diskUsage(c.diskPath); ok {
			s.DiskUsed, s.DiskTotal = used, total
			if total > 0 {
				s.DiskPercent = round1(float64(used) / float64(total) * 100)
			}
		}
	}

	return s
}

// cpuPercent derives utilisation from the delta between two /proc/stat reads,
// which is the only way to get it -- the file holds cumulative jiffies, not a
// rate. The first call has no previous sample and reports 0.
func (c *Collector) cpuPercent() float64 {
	busy, idle, ok := c.cpuTimes()
	if !ok {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	prevBusy, prevIdle, hadPrev := c.lastBusy, c.lastIdle, c.hasPrev
	c.lastBusy, c.lastIdle, c.hasPrev = busy, idle, true

	if !hadPrev {
		return 0
	}

	deltaBusy := float64(busy - prevBusy)
	deltaTotal := deltaBusy + float64(idle-prevIdle)
	if deltaTotal <= 0 {
		return 0
	}
	return round1(deltaBusy / deltaTotal * 100)
}

// cpuTimes returns cumulative busy and idle jiffies from the aggregate "cpu"
// line of /proc/stat.
func (c *Collector) cpuTimes() (busy, idle uint64, ok bool) {
	f, err := os.Open(filepath.Join(c.procRoot, "stat"))
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, 0, false
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 8 || fields[0] != "cpu" {
		return 0, 0, false
	}

	var total uint64
	for i, field := range fields[1:] {
		v, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += v
		// Fields 4 and 5 are idle and iowait; both count as not-busy.
		if i == 3 || i == 4 {
			idle += v
		}
	}

	return total - idle, idle, true
}

// memory returns total and available bytes. MemAvailable is the kernel's own
// estimate of what a new workload could claim, which is a far better "free"
// than MemFree.
func (c *Collector) memory() (total, available uint64, ok bool) {
	f, err := os.Open(filepath.Join(c.procRoot, "meminfo"))
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	var gotTotal, gotAvailable bool
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		key, value, found := strings.Cut(scanner.Text(), ":")
		if !found {
			continue
		}

		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}

		switch key {
		case "MemTotal":
			total, gotTotal = kb*1024, true
		case "MemAvailable":
			available, gotAvailable = kb*1024, true
		}
		if gotTotal && gotAvailable {
			break
		}
	}

	return total, available, gotTotal && gotAvailable
}

func (c *Collector) loadAvg() (l1, l5, l15 float64, ok bool) {
	b, err := os.ReadFile(filepath.Join(c.procRoot, "loadavg"))
	if err != nil {
		return 0, 0, 0, false
	}

	fields := strings.Fields(string(b))
	if len(fields) < 3 {
		return 0, 0, 0, false
	}

	values := make([]float64, 3)
	for i := range values {
		if values[i], err = strconv.ParseFloat(fields[i], 64); err != nil {
			return 0, 0, 0, false
		}
	}
	return values[0], values[1], values[2], true
}

func (c *Collector) uptime() (uint64, bool) {
	b, err := os.ReadFile(filepath.Join(c.procRoot, "uptime"))
	if err != nil {
		return 0, false
	}

	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, false
	}

	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return uint64(seconds), true
}

func diskUsage(path string) (used, total uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, false
	}

	total = st.Blocks * uint64(st.Bsize)
	// Available, not Free: Bfree includes blocks reserved for root.
	used = total - st.Bavail*uint64(st.Bsize)
	return used, total, true
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
