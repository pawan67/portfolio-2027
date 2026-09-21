package listening

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func testPoller(t *testing.T, lastfmBody, itunesBody string) (*Poller, func()) {
	t.Helper()

	lastfm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, lastfmBody)
	}))
	itunes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, itunesBody)
	}))

	p := New("someone", "key", slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.lastfm.base = lastfm.URL
	p.itunes.base = itunes.URL

	return p, func() {
		lastfm.Close()
		itunes.Close()
	}
}

const nowPlayingJSON = `{"recenttracks":{"track":[
  {"artist":{"#text":"Jai Paul"},"name":"Str8 Outta Mumbai","album":{"#text":"Leak 04-13"},
   "url":"https://www.last.fm/music/Jai+Paul/_/Str8+Outta+Mumbai","@attr":{"nowplaying":"true"}},
  {"artist":{"#text":"Burial"},"name":"Archangel","album":{"#text":"Untrue"},
   "url":"https://www.last.fm/music/Burial/_/Archangel"}
]}}`

const previewJSON = `{"resultCount":1,"results":[
  {"trackName":"Str8 Outta Mumbai","artistName":"Jai Paul",
   "previewUrl":"https://audio.example/clip.m4a"}
]}`

func TestRecentReadsNowPlayingFromArray(t *testing.T) {
	p, done := testPoller(t, nowPlayingJSON, previewJSON)
	defer done()

	got, err := p.lastfm.recent(context.Background())
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if !got.Playing {
		t.Error("expected the first entry to be flagged as now playing")
	}
	if got.Title != "Str8 Outta Mumbai" || got.Artist != "Jai Paul" {
		t.Errorf("got %q by %q", got.Title, got.Artist)
	}
	if got.Album != "Leak 04-13" {
		t.Errorf("album = %q", got.Album)
	}
}

// A user whose history is a single scrobble comes back as a bare object rather
// than a one-element list. Decoding straight into a slice would fail here.
func TestRecentReadsSingleObjectShape(t *testing.T) {
	body := `{"recenttracks":{"track":
	  {"artist":{"#text":"Burial"},"name":"Archangel","album":{"#text":"Untrue"},
	   "url":"https://www.last.fm/music/Burial/_/Archangel"}}}`

	p, done := testPoller(t, body, previewJSON)
	defer done()

	got, err := p.lastfm.recent(context.Background())
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if got.Playing {
		t.Error("a scrobble with no nowplaying attribute must not report as playing")
	}
	if got.Artist != "Burial" {
		t.Errorf("artist = %q", got.Artist)
	}
}

// Last.fm answers a bad key or unknown user with HTTP 200 and an error object.
// Treating that as success would wipe the cached track on every poll.
func TestRecentRejectsErrorBodyServedAs200(t *testing.T) {
	p, done := testPoller(t, `{"error":6,"message":"User not found"}`, previewJSON)
	defer done()

	if _, err := p.lastfm.recent(context.Background()); err == nil {
		t.Fatal("expected an error for a Last.fm error body")
	}
}

func TestPreviewAcceptsLooseTitleMatch(t *testing.T) {
	body := `{"resultCount":1,"results":[
	  {"trackName":"Archangel (Remastered 2019)","artistName":"Burial",
	   "previewUrl":"https://audio.example/clip.m4a"}]}`

	p, done := testPoller(t, nowPlayingJSON, body)
	defer done()

	url, err := p.itunes.preview(context.Background(), "Burial", "Archangel")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if url != "https://audio.example/clip.m4a" {
		t.Errorf("url = %q, want the clip", url)
	}
}

// iTunes ranks fuzzily and the top hit is regularly a karaoke or tribute
// recording. Playing the wrong song is worse than playing nothing.
func TestPreviewRejectsDifferentArtist(t *testing.T) {
	body := `{"resultCount":2,"results":[
	  {"trackName":"Archangel","artistName":"Karaoke Allstars",
	   "previewUrl":"https://audio.example/wrong.m4a"},
	  {"trackName":"Something Else","artistName":"Burial",
	   "previewUrl":"https://audio.example/alsowrong.m4a"}]}`

	p, done := testPoller(t, nowPlayingJSON, body)
	defer done()

	url, err := p.itunes.preview(context.Background(), "Burial", "Archangel")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if url != "" {
		t.Errorf("url = %q, want none", url)
	}
}

func TestRefreshPublishesTrackWithProxiedPreview(t *testing.T) {
	p, done := testPoller(t, nowPlayingJSON, previewJSON)
	defer done()

	p.refresh(context.Background())

	got, ok := p.Current()
	if !ok {
		t.Fatal("no track cached after refresh")
	}
	if !strings.HasPrefix(got.Preview, "/api/listening/preview/") ||
		!strings.HasSuffix(got.Preview, ".m4a") {
		t.Errorf("preview = %q, want a path on this origin", got.Preview)
	}
	// The upstream URL must never reach the browser: it is what keeps the
	// audio endpoint from being pointable at an arbitrary destination.
	if strings.Contains(got.Preview, "audio.example") {
		t.Error("published preview leaked the upstream URL")
	}
}

// A Last.fm blip should leave the footer showing a slightly stale track rather
// than blanking it and popping back.
func TestRefreshKeepsLastGoodTrackOnFailure(t *testing.T) {
	p, done := testPoller(t, nowPlayingJSON, previewJSON)
	defer done()

	p.refresh(context.Background())
	before, _ := p.Current()

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer broken.Close()
	p.lastfm.base = broken.URL

	p.refresh(context.Background())

	after, ok := p.Current()
	if !ok || after.Title != before.Title {
		t.Errorf("after a failed poll: %+v, want %+v", after, before)
	}
}

func TestRememberEvictsOldestBeyondCap(t *testing.T) {
	p := New("someone", "key", slog.New(slog.NewTextHandler(io.Discard, nil)))

	first := p.remember("https://audio.example/track-0.m4a")
	for i := 1; i <= maxPreviews; i++ {
		p.remember("https://audio.example/track-" + strconv.Itoa(i) + ".m4a")
	}

	if _, ok := p.previewURL(first); ok {
		t.Error("the oldest key should have been evicted")
	}
	if len(p.previews) > maxPreviews {
		t.Errorf("%d keys retained, cap is %d", len(p.previews), maxPreviews)
	}
}

func TestServeReturnsNoContentWhenUnconfigured(t *testing.T) {
	p := New("", "", slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := httptest.NewRecorder()
	Serve(p).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/listening", nil))

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestServePublishesCurrentTrack(t *testing.T) {
	p, done := testPoller(t, nowPlayingJSON, previewJSON)
	defer done()
	p.refresh(context.Background())

	rec := httptest.NewRecorder()
	Serve(p).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/listening", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got track
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Title != "Str8 Outta Mumbai" || !got.Playing {
		t.Errorf("published %+v", got)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "s-maxage=20") {
		t.Errorf("Cache-Control = %q", cc)
	}
}

func TestPreviewRejectsUnmintedKey(t *testing.T) {
	p := New("someone", "key", slog.New(slog.NewTextHandler(io.Discard, nil)))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/listening/preview/deadbeefdeadbeef.m4a", nil)
	req.SetPathValue("clip", "deadbeefdeadbeef.m4a")
	Preview(p).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestPreviewStreamsMintedKey(t *testing.T) {
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-m4p")
		_, _ = io.WriteString(w, "not really aac")
	}))
	defer audio.Close()

	p := New("someone", "key", slog.New(slog.NewTextHandler(io.Discard, nil)))
	key := p.remember(audio.URL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/listening/preview/"+key+".m4a", nil)
	req.SetPathValue("clip", key+".m4a")
	Preview(p).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "not really aac" {
		t.Errorf("body = %q", got)
	}
	// Apple serves these as audio/x-m4p, which browsers refuse to decode.
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mp4" {
		t.Errorf("Content-Type = %q, want the corrected audio/mp4", ct)
	}
}
