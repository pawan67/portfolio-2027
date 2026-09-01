package rum

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// A beacon carries at most five metrics; anything larger is not ours.
	maxBodyBytes = 2048
	// Per-IP fixed window. Generous for a real visitor, cheap to abuse-proof.
	rateLimit  = 60
	rateWindow = time.Minute
	// Beyond this many tracked IPs in one window, stop admitting new ones.
	// Bounds memory against spoofed source addresses.
	maxTrackedIPs = 20000
	// Values beyond these bounds are broken clients, not slow ones.
	maxTimingMs = 120000
	maxCLS      = 10
)

// beacon is the payload the browser posts.
type beacon struct {
	Metrics []struct {
		Name  string  `json:"name"`
		Value float64 `json:"value"`
	} `json:"metrics"`
}

// Ingest returns the handler for POST /api/rum.
func Ingest(store *Store, now func() time.Time) http.Handler {
	limiter := newLimiter()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limiter.allow(clientIP(r), now()) {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}

		var b beacon
		if err := json.Unmarshal(body, &b); err != nil {
			http.Error(w, "malformed payload", http.StatusBadRequest)
			return
		}

		// Cloudflare supplies the country. No IP address is stored, and the
		// header is only trustworthy behind the proxy -- direct origin hits
		// simply fall through to "unknown".
		country := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
		if len(country) != 2 {
			country = "??"
		}

		ts := now()
		accepted := 0
		for _, m := range b.Metrics {
			if !validMetric(m.Name) || !plausible(m.Name, m.Value) {
				continue
			}
			store.Add(m.Name, country, m.Value, ts)
			accepted++
		}

		if accepted == 0 {
			http.Error(w, "no valid metrics", http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// Serve returns the handler for GET /api/perf.
func Serve(store *Store, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		report := store.Report(now())

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// Short edge cache: fresh enough to feel live, long enough that a
		// traffic spike does not turn into a report-generation spike.
		w.Header().Set("Cache-Control", "public, max-age=60, s-maxage=60")
		_ = json.NewEncoder(w).Encode(report)
	})
}

func validMetric(name string) bool {
	for _, m := range Metrics {
		if m == name {
			return true
		}
	}
	return false
}

// plausible rejects values a real browser would not produce.
func plausible(metric string, v float64) bool {
	if v < 0 {
		return false
	}
	if metric == MetricCLS {
		return v <= maxCLS
	}
	return v <= maxTimingMs
}

// clientIP prefers Cloudflare's header, then the first X-Forwarded-For hop,
// then the socket address.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if first, _, ok := strings.Cut(fwd, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiter is a fixed-window per-IP counter. The whole window is discarded on
// roll-over, which keeps it allocation-light and self-cleaning.
type limiter struct {
	mu          sync.Mutex
	counts      map[string]int
	windowStart time.Time
}

func newLimiter() *limiter {
	return &limiter{counts: map[string]int{}}
}

func (l *limiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.windowStart) >= rateWindow {
		l.counts = make(map[string]int, len(l.counts))
		l.windowStart = now
	}

	n, seen := l.counts[ip]
	if !seen && len(l.counts) >= maxTrackedIPs {
		return false
	}
	if n >= rateLimit {
		return false
	}

	l.counts[ip] = n + 1
	return true
}
