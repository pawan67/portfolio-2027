// Package rum collects and reports field performance data from real visitors.
//
// Samples are aggregated into fixed-bucket histograms rather than stored
// individually. That is how CrUX reports Core Web Vitals, and it means the
// origin keeps no per-visitor rows, needs no database, and answers a percentile
// query in constant time.
package rum

import "math"

// Metric names accepted from the beacon.
const (
	MetricLCP  = "LCP"
	MetricINP  = "INP"
	MetricCLS  = "CLS"
	MetricTTFB = "TTFB"
	MetricFCP  = "FCP"
)

// Metrics is the accepted set, in display order.
var Metrics = []string{MetricLCP, MetricINP, MetricCLS, MetricTTFB, MetricFCP}

// Google's Core Web Vitals thresholds. Values are milliseconds except CLS,
// which is unitless.
var thresholds = map[string][2]float64{
	MetricLCP:  {2500, 4000},
	MetricINP:  {200, 500},
	MetricCLS:  {0.1, 0.25},
	MetricTTFB: {800, 1800},
	MetricFCP:  {1800, 3000},
}

var (
	timingEdges = buildEdges(25, 1.15, 20000)
	clsEdges    = buildEdges(0.005, 1.2, 2)
)

// buildEdges returns geometrically spaced bucket lower bounds starting at 0.
// Geometric spacing keeps relative error roughly constant across the range,
// which matters because a 50ms error is significant at 200ms and irrelevant at
// 8s.
func buildEdges(first, ratio, limit float64) []float64 {
	edges := []float64{0}
	for v := first; v < limit; v *= ratio {
		edges = append(edges, v)
	}
	return edges
}

func edgesFor(metric string) []float64 {
	if metric == MetricCLS {
		return clsEdges
	}
	return timingEdges
}

// Rating buckets a value as good, needs-improvement, or poor.
func Rating(metric string, value float64) string {
	t, ok := thresholds[metric]
	if !ok {
		return "unknown"
	}
	switch {
	case value <= t[0]:
		return "good"
	case value <= t[1]:
		return "needs-improvement"
	default:
		return "poor"
	}
}

// Histogram is a counter per bucket for one metric.
type Histogram struct {
	Metric string `json:"metric"`
	Counts []int  `json:"counts"`
}

// NewHistogram allocates a histogram sized for the metric's bucket layout.
func NewHistogram(metric string) *Histogram {
	return &Histogram{Metric: metric, Counts: make([]int, len(edgesFor(metric)))}
}

// Add records one observation.
func (h *Histogram) Add(value float64) {
	if math.IsNaN(value) || value < 0 {
		return
	}

	edges := edgesFor(h.Metric)

	// Walk down from the top: the last edge not greater than value.
	i := len(edges) - 1
	for i > 0 && edges[i] > value {
		i--
	}
	h.Counts[i]++
}

// Total returns the number of observations.
func (h *Histogram) Total() int {
	n := 0
	for _, c := range h.Counts {
		n += c
	}
	return n
}

// Merge folds other into h. Both must be for the same metric.
func (h *Histogram) Merge(other *Histogram) {
	for i, c := range other.Counts {
		if i < len(h.Counts) {
			h.Counts[i] += c
		}
	}
}

// FractionBelow returns the share of observations at or below v.
// Buckets are counted whole, so the result is exact only at a bucket boundary --
// which is what the Core Web Vitals thresholds are chosen against anyway.
func (h *Histogram) FractionBelow(v float64) float64 {
	total := h.Total()
	if total == 0 {
		return 0
	}

	edges := edgesFor(h.Metric)
	below := 0
	for i, c := range h.Counts {
		if edges[i] >= v {
			break
		}
		below += c
	}
	return float64(below) / float64(total)
}

// Quantile estimates the value at q (0..1), interpolating within the bucket
// that contains it. Returns 0 for an empty histogram.
func (h *Histogram) Quantile(q float64) float64 {
	total := h.Total()
	if total == 0 {
		return 0
	}

	edges := edgesFor(h.Metric)
	target := q * float64(total)

	cumulative := 0
	for i, c := range h.Counts {
		if c == 0 {
			continue
		}
		if float64(cumulative+c) < target {
			cumulative += c
			continue
		}

		lo := edges[i]
		hi := lo * 1.15
		if i+1 < len(edges) {
			hi = edges[i+1]
		}

		// Position within this bucket, assuming a uniform spread inside it.
		frac := (target - float64(cumulative)) / float64(c)
		return lo + frac*(hi-lo)
	}

	return edges[len(edges)-1]
}
