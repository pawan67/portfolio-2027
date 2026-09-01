package rum

import (
	"sort"
	"time"
)

// MetricSummary is one metric's field performance over the window.
type MetricSummary struct {
	Metric  string  `json:"metric"`
	P50     float64 `json:"p50"`
	P75     float64 `json:"p75"`
	P95     float64 `json:"p95"`
	Samples int     `json:"samples"`
	// Rating applies the Core Web Vitals thresholds to p75, which is the
	// statistic Google assesses against.
	Rating string `json:"rating"`
	// GoodShare is the fraction of individual samples rated good, which is a
	// fairer picture than p75 alone when the distribution is bimodal.
	GoodShare float64 `json:"goodShare"`
	Unit      string  `json:"unit"`
}

// CountrySummary breaks LCP down by region.
type CountrySummary struct {
	Country string  `json:"country"`
	Samples int     `json:"samples"`
	LCPP75  float64 `json:"lcpP75"`
	Rating  string  `json:"rating"`
}

// Report is the payload served at /api/perf.
type Report struct {
	WindowDays  int              `json:"windowDays"`
	GeneratedAt time.Time        `json:"generatedAt"`
	Samples     int              `json:"samples"`
	Metrics     []MetricSummary  `json:"metrics"`
	Countries   []CountrySummary `json:"countries"`
}

// Report aggregates the rolling window into the shape the perf page renders.
func (s *Store) Report(now time.Time) Report {
	cutoff := now.UTC().AddDate(0, 0, -s.windowDays).Format(dayFormat)

	s.mu.RLock()
	defer s.mu.RUnlock()

	byMetric := map[string]*Histogram{}
	byCountry := map[string]*Histogram{}
	total := 0

	for k, h := range s.data {
		if k.Day < cutoff {
			continue
		}

		if _, ok := byMetric[k.Metric]; !ok {
			byMetric[k.Metric] = NewHistogram(k.Metric)
		}
		byMetric[k.Metric].Merge(h)
		total += h.Total()

		if k.Metric == MetricLCP {
			if _, ok := byCountry[k.Country]; !ok {
				byCountry[k.Country] = NewHistogram(MetricLCP)
			}
			byCountry[k.Country].Merge(h)
		}
	}

	report := Report{
		WindowDays:  s.windowDays,
		GeneratedAt: now.UTC(),
		Samples:     total,
		Metrics:     make([]MetricSummary, 0, len(Metrics)),
		Countries:   make([]CountrySummary, 0, len(byCountry)),
	}

	// Metrics keep their declared order so the page layout is stable even as
	// data arrives for some metrics before others.
	for _, name := range Metrics {
		h, ok := byMetric[name]
		if !ok || h.Total() == 0 {
			continue
		}

		p75 := h.Quantile(0.75)
		unit := "ms"
		if name == MetricCLS {
			unit = ""
		}

		report.Metrics = append(report.Metrics, MetricSummary{
			Metric:    name,
			P50:       round(h.Quantile(0.50), name),
			P75:       round(p75, name),
			P95:       round(h.Quantile(0.95), name),
			Samples:   h.Total(),
			Rating:    Rating(name, p75),
			GoodShare: float64(int(h.FractionBelow(thresholds[name][0])*1000)) / 1000,
			Unit:      unit,
		})
	}

	for country, h := range byCountry {
		p75 := h.Quantile(0.75)
		report.Countries = append(report.Countries, CountrySummary{
			Country: country,
			Samples: h.Total(),
			LCPP75:  round(p75, MetricLCP),
			Rating:  Rating(MetricLCP, p75),
		})
	}

	// Busiest first, then alphabetical so ties do not reorder between requests.
	sort.Slice(report.Countries, func(i, j int) bool {
		if report.Countries[i].Samples != report.Countries[j].Samples {
			return report.Countries[i].Samples > report.Countries[j].Samples
		}
		return report.Countries[i].Country < report.Countries[j].Country
	})

	return report
}

// round trims false precision: sub-millisecond timings and sub-0.001 CLS are
// noise given the bucket widths.
func round(v float64, metric string) float64 {
	if metric == MetricCLS {
		return float64(int(v*1000+0.5)) / 1000
	}
	return float64(int(v + 0.5))
}
