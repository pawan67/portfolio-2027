package assets

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func testSet(t *testing.T) *Set {
	t.Helper()

	fsys := fstest.MapFS{
		// Sizes are deliberate: br is smallest, then zstd, then gzip.
		"index.html":        {Data: []byte("<html>home</html>")},
		"index.html.br":     {Data: []byte("br")},
		"index.html.zst":    {Data: []byte("zstd")},
		"index.html.gz":     {Data: []byte("gzip!!")},
		"about/index.html":  {Data: []byte("<html>about</html>")},
		"404/index.html":    {Data: []byte("<html>missing</html>")},
		"_a/app.Bx1.css":    {Data: []byte("body{}")},
		"_a/app.Bx1.css.br": {Data: []byte("br-css")},
		"_a/inter.woff2":    {Data: []byte("font")},
	}

	s, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return s
}

func get(t *testing.T, s *Set, method, target, acceptEncoding, ifNoneMatch string) *http.Response {
	t.Helper()

	req := httptest.NewRequest(method, target, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Result()
}

func TestRouteAliases(t *testing.T) {
	s := testSet(t)

	for _, target := range []string{"/", "/index.html", "/about", "/about/", "/about/index.html"} {
		resp := get(t, s, http.MethodGet, target, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", target, resp.StatusCode)
		}
	}
}

func TestEncodingPreference(t *testing.T) {
	s := testSet(t)

	tests := []struct {
		name     string
		accept   string
		wantEnc  string
		wantBody string
	}{
		{"no header falls back to identity", "", "", "<html>home</html>"},
		{"only gzip offered", "gzip", "gzip", "gzip!!"},
		{"smallest of the accepted set wins", "gzip, br, zstd", "br", "br"},
		{"smallest wins even when listed last", "gzip, zstd", "zstd", "zstd"},
		{"explicit q-value outranks size", "br;q=0.1, gzip;q=0.9", "gzip", "gzip!!"},
		{"q=0 refuses an encoding", "br;q=0, zstd", "zstd", "zstd"},
		{"wildcard accepts anything", "*", "br", "br"},
		{"identity only", "identity", "", "<html>home</html>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := get(t, s, http.MethodGet, "/", tc.accept, "")

			if got := resp.Header.Get("Content-Encoding"); got != tc.wantEnc {
				t.Errorf("encoding = %q, want %q", got, tc.wantEnc)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if string(body) != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

func TestVaryAlwaysSet(t *testing.T) {
	s := testSet(t)
	resp := get(t, s, http.MethodGet, "/", "br", "")

	if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q, want Accept-Encoding", got)
	}
}

// Each encoding is a distinct representation, so sharing one validator across
// them would let a cache hand a br body to a client that asked for identity.
func TestETagVariesByEncoding(t *testing.T) {
	s := testSet(t)

	identity := get(t, s, http.MethodGet, "/", "", "").Header.Get("ETag")
	brotli := get(t, s, http.MethodGet, "/", "br", "").Header.Get("ETag")

	if identity == "" || brotli == "" {
		t.Fatal("ETag missing")
	}
	if identity == brotli {
		t.Errorf("ETag %q shared between identity and br", identity)
	}
}

func TestConditionalRequest(t *testing.T) {
	s := testSet(t)

	etag := get(t, s, http.MethodGet, "/", "br", "").Header.Get("ETag")

	resp := get(t, s, http.MethodGet, "/", "br", etag)
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("status = %d, want 304", resp.StatusCode)
	}

	stale := get(t, s, http.MethodGet, "/", "br", `"deadbeef-br"`)
	if stale.StatusCode != http.StatusOK {
		t.Errorf("stale ETag: status = %d, want 200", stale.StatusCode)
	}
}

func TestCacheControl(t *testing.T) {
	s := testSet(t)

	hashed := get(t, s, http.MethodGet, "/_a/app.Bx1.css", "", "").Header.Get("Cache-Control")
	if hashed != "public, max-age=31536000, immutable" {
		t.Errorf("hashed asset Cache-Control = %q", hashed)
	}

	html := get(t, s, http.MethodGet, "/", "", "").Header.Get("Cache-Control")
	if html == hashed {
		t.Errorf("HTML must not be immutable, got %q", html)
	}
}

func TestPreloadLinkOnHTMLOnly(t *testing.T) {
	s := testSet(t)

	if link := get(t, s, http.MethodGet, "/", "", "").Header.Get("Link"); link == "" {
		t.Error("HTML response missing Link preload header")
	}
	if link := get(t, s, http.MethodGet, "/_a/app.Bx1.css", "", "").Header.Get("Link"); link != "" {
		t.Errorf("non-HTML response should not carry Link, got %q", link)
	}
}

func TestNotFoundServesErrorPage(t *testing.T) {
	s := testSet(t)
	resp := get(t, s, http.MethodGet, "/nope", "", "")

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct == "" {
		t.Error("404 response missing Content-Type")
	}
}

func TestHeadHasNoBody(t *testing.T) {
	s := testSet(t)
	resp := get(t, s, http.MethodHead, "/", "", "")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if resp.ContentLength == 0 {
		t.Error("HEAD should still advertise Content-Length")
	}
	buf := make([]byte, 1)
	if n, _ := resp.Body.Read(buf); n != 0 {
		t.Error("HEAD returned a body")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	s := testSet(t)
	resp := get(t, s, http.MethodPost, "/", "", "")

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != "GET, HEAD" {
		t.Errorf("Allow = %q", allow)
	}
}
