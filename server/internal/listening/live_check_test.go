package listening

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Guarded by an env var: hits the real iTunes Search API. Run with
// LIVE_ITUNES=1 go test -run TestLiveITunes ./internal/listening/
func TestLiveITunes(t *testing.T) {
	if os.Getenv("LIVE_ITUNES") == "" {
		t.Skip("set LIVE_ITUNES=1 to hit the real API")
	}
	p := New("x", "y", slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, c := range [][2]string{
		{"Burial", "Archangel"},
		{"Jai Paul", "Str8 Outta Mumbai"},
		{"Fred again..", "Delilah (pull me out of this)"},
		{"Radiohead", "Everything In Its Right Place"},
		{"A.R. Rahman", "Kun Faya Kun"},
		{"Nonexistent Band Qqzz", "No Such Song"},
	} {
		url, err := p.itunes.preview(context.Background(), c[0], c[1])
		t.Logf("%-14s %-38s -> err=%v url=%.72s", c[0], c[1], err, url)
	}
}

// End-to-end over the real iTunes API and a stubbed Last.fm: poll, publish,
// then pull the sample back through the proxy the browser would use. Proves
// the minted key resolves to real audio bytes served from this origin.
func TestLiveEndToEnd(t *testing.T) {
	if os.Getenv("LIVE_ITUNES") == "" {
		t.Skip("set LIVE_ITUNES=1 to hit the real API")
	}

	lastfm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, nowPlayingJSON)
	}))
	defer lastfm.Close()

	p := New("someone", "key", slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.lastfm.base = lastfm.URL
	p.refresh(context.Background())

	rec := httptest.NewRecorder()
	Serve(p).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/listening", nil))

	var published track
	if err := json.Unmarshal(rec.Body.Bytes(), &published); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Logf("published: %+v", published)
	if published.Preview == "" {
		t.Fatal("no preview resolved for a track that has one")
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, published.Preview, nil)
	req.SetPathValue("clip", strings.TrimPrefix(published.Preview, "/api/listening/preview/"))
	Preview(p).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d", rec.Code)
	}
	body := rec.Body.Bytes()
	t.Logf("preview: %d bytes, type %q", len(body), rec.Header().Get("Content-Type"))
	if len(body) < 100_000 {
		t.Errorf("preview is %d bytes, expected a real 30-second clip", len(body))
	}
	// Every MPEG-4 container opens with a box-size word then "ftyp".
	if len(body) < 12 || string(body[4:8]) != "ftyp" {
		t.Errorf("preview does not look like an MP4/AAC container")
	}
}
