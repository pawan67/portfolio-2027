package listening

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// A 30-second AAC clip is comfortably under 1MB. The ceiling exists so an
	// unexpected upstream response cannot stream through here unbounded.
	maxPreviewBytes = 8 << 20

	// Long enough for the whole clip on a bad connection, short enough that a
	// stalled upstream releases the goroutine.
	previewTimeout = 60 * time.Second

	// Each chunk written pushes the connection deadline forward; the server's
	// 20s WriteTimeout would otherwise cut off a slow but honest download.
	previewWriteTimeout = 15 * time.Second
)

// Serve returns the handler for GET /api/listening.
//
// 204 is the answer whenever there is nothing to show -- the feature is
// unconfigured, or the first poll has not landed yet. The footer treats that as
// "remove yourself from the page", which is the right outcome for a deployment
// with no Last.fm credentials: no empty row, no broken control, no console
// noise about a 404.
func Serve(p *Poller) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		if !p.Enabled() {
			// Unconfigured is a property of the build, not of the minute, so
			// let the edge answer this for everyone.
			h.Set("Cache-Control", "public, max-age=3600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		current, ok := p.Current()
		if !ok {
			// Only true for the few seconds between boot and the first poll.
			h.Set("Cache-Control", "public, max-age=5")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		h.Set("Content-Type", "application/json; charset=utf-8")
		// Matched to the poll interval: caching for longer would publish a
		// staleness the origin does not have, and for less would make the edge
		// pointless without making the answer any fresher.
		h.Set("Cache-Control", "public, max-age=20, s-maxage=20")
		_ = json.NewEncoder(w).Encode(current)
	})
}

// Preview returns the handler for GET /api/listening/preview/{clip}.
//
// It proxies rather than redirects, for two reasons. The page keeps a
// `media-src 'self'` CSP, which a redirect to Apple's CDN would violate; and
// the key in the path is one this process minted for a URL it chose, so the
// endpoint cannot be pointed at an arbitrary destination the way a `?url=`
// proxy could.
//
// The key sits in the path with an .m4a suffix rather than in a query string
// because Cloudflare's default cache rules key on file extension: as
// `?k=...` every play would come off the VPS, and a 30-second AAC clip is the
// better part of a megabyte.
func Preview(p *Poller) http.Handler {
	client := &http.Client{Timeout: previewTimeout}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream, ok := p.previewURL(strings.TrimSuffix(r.PathValue("clip"), ".m4a"))
		if !ok {
			http.Error(w, "unknown preview", http.StatusNotFound)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream, nil)
		if err != nil {
			http.Error(w, "bad preview", http.StatusInternalServerError)
			return
		}
		req.Header.Set("User-Agent", userAgent)
		// Forwarded so the browser can seek within the clip instead of
		// re-downloading it from the start.
		if rng := r.Header.Get("Range"); rng != "" {
			req.Header.Set("Range", rng)
		}

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "preview unavailable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			http.Error(w, "preview unavailable", http.StatusBadGateway)
			return
		}

		h := w.Header()
		for _, name := range []string{"Content-Length", "Content-Range", "Accept-Ranges"} {
			if v := resp.Header.Get(name); v != "" {
				h.Set(name, v)
			}
		}
		// Corrected rather than passed through. Apple labels these
		// audio/x-m4p, the protected-iTunes type, which no browser will decode
		// -- and the origin sets X-Content-Type-Options: nosniff globally, so
		// there is no fallback to container sniffing. They are ordinary
		// AAC-in-MP4 previews; audio/mp4 is what they actually are.
		h.Set("Content-Type", "audio/mp4")
		// The key is derived from the upstream URL, so a given key always
		// resolves to the same bytes. That makes this safely immutable, and a
		// re-listen costs nothing.
		h.Set("Cache-Control", "public, max-age=86400, immutable")

		w.WriteHeader(resp.StatusCode)

		dst := deadlineWriter{
			w:  w,
			rc: http.NewResponseController(w),
			d:  previewWriteTimeout,
		}
		// An error here means the listener navigated away mid-clip, which is
		// the normal way a 30-second sample ends.
		_, _ = io.CopyN(dst, resp.Body, maxPreviewBytes)
	})
}

// deadlineWriter pushes the connection's write deadline forward on every chunk.
//
// A ResponseWriter that does not support deadlines is not an error worth
// failing the response over -- the copy proceeds and the server's own
// WriteTimeout applies, which is what would have happened anyway.
type deadlineWriter struct {
	w  io.Writer
	rc *http.ResponseController
	d  time.Duration
}

func (dw deadlineWriter) Write(p []byte) (int, error) {
	_ = dw.rc.SetWriteDeadline(time.Now().Add(dw.d))
	return dw.w.Write(p)
}
