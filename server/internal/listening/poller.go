package listening

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	// Last.fm marks a track "now playing" as soon as the scrobbler reports it,
	// so the only lag a visitor sees is this interval plus the edge cache.
	// Three requests a minute sits far under Last.fm's published ceiling, and
	// it is a fixed cost: polling here rather than per-request means a traffic
	// spike cannot turn into an API spike.
	PollInterval = 20 * time.Second

	// Upstream calls run on a background goroutine, so a slow response delays
	// the next sample rather than a visitor's page. Bounded anyway -- a hung
	// connection would otherwise pin the loop indefinitely.
	upstreamTimeout = 6 * time.Second

	// Preview URLs are remembered per track so the audio endpoint never takes a
	// URL from the caller; it takes a key that this process minted. Past this
	// many distinct tracks the oldest are dropped. It only has to outlive open
	// tabs, not the process.
	maxPreviews = 64

	userAgent = "portfolio-2027 (+https://github.com/pawan67/portfolio-2027)"

	lastfmEndpoint = "https://ws.audioscrobbler.com/2.0/"
	itunesEndpoint = "https://itunes.apple.com/search"
)

// Poller keeps one cached answer and refreshes it on a timer. Every request
// reads that cache; none of them reach an upstream.
type Poller struct {
	log      *slog.Logger
	lastfm   *lastfmClient
	itunes   *itunesClient
	interval time.Duration

	mu       sync.RWMutex
	current  track
	have     bool
	previews map[string]string
	// Insertion order, for bounded eviction. A ring would be tidier; at 64
	// entries a slice is cheaper to read than to justify.
	order []string
}

// New returns a poller for one Last.fm user. An empty user or key disables the
// feature: Run returns immediately and the handlers answer 204, so the footer
// takes itself off the page rather than showing a broken control.
func New(user, apiKey string, log *slog.Logger) *Poller {
	// One client, one connection pool, shared by both upstreams. The timeout
	// covers the whole exchange including the body, which is the bound that
	// actually matters for a loop that must not stall.
	client := &http.Client{Timeout: upstreamTimeout}

	return &Poller{
		log:      log,
		lastfm:   &lastfmClient{http: client, base: lastfmEndpoint, user: user, apiKey: apiKey},
		itunes:   &itunesClient{http: client, base: itunesEndpoint, country: "US"},
		interval: PollInterval,
		previews: map[string]string{},
	}
}

// Enabled reports whether credentials were supplied.
func (p *Poller) Enabled() bool {
	return p.lastfm.user != "" && p.lastfm.apiKey != ""
}

// Run refreshes until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	if !p.Enabled() {
		return
	}

	// Fetch once up front so the first visitor after a deploy sees a track
	// instead of waiting out a full interval for the first tick.
	p.refresh(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.refresh(ctx)
		}
	}
}

// refresh replaces the cached track only on success. A network blip or a
// Last.fm outage leaves the previous answer in place: a footer line that is a
// minute stale reads better than one that blanks and comes back.
func (p *Poller) refresh(ctx context.Context) {
	next, err := p.lastfm.recent(ctx)
	if err != nil {
		p.log.Warn("listening: last.fm poll failed", "err", err)
		return
	}

	// Resolving the sample is a second network call, so it is only made when
	// the track actually changed. Repeats of the same song -- and the long
	// stretches where nothing is playing at all -- cost one request, not two.
	if prev, ok := p.Current(); ok && prev.key() == next.key() {
		next.Preview = prev.Preview
		p.store(next)
		return
	}

	if url, err := p.itunes.preview(ctx, next.Artist, next.Title); err != nil {
		// Not fatal, and not worth a warning every twenty seconds: the track
		// still publishes, just without a play control.
		p.log.Debug("listening: preview lookup failed", "err", err, "track", next.Title)
	} else if url != "" {
		next.Preview = "/api/listening/preview/" + p.remember(url) + ".m4a"
	}

	p.store(next)
}

func (p *Poller) store(t track) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current, p.have = t, true
}

// Current returns the cached track. The bool is false until the first
// successful poll.
func (p *Poller) Current() (track, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.current, p.have
}

// remember registers an upstream preview URL under an opaque key and returns
// it. The audio endpoint resolves keys and nothing else, which is what keeps it
// from being an open proxy: a caller cannot name a destination, only pick one
// this process already chose.
//
// The key is derived from the URL rather than random, so the same track always
// maps to the same key -- a page left open across a restart keeps working once
// the poller has seen that track again.
func (p *Poller) remember(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	key := hex.EncodeToString(sum[:8])

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.previews[key]; !exists {
		if len(p.order) >= maxPreviews {
			delete(p.previews, p.order[0])
			p.order = p.order[1:]
		}
		p.order = append(p.order, key)
	}
	p.previews[key] = rawURL
	return key
}

// previewURL resolves a key minted by remember.
func (p *Poller) previewURL(key string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	url, ok := p.previews[key]
	return url, ok
}
