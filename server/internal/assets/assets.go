// Package assets serves a pre-built, pre-compressed static site out of an
// embedded filesystem.
//
// Everything is loaded and hashed once at startup, so a request costs a map
// lookup, a content-negotiation pass, and a write. Nothing touches disk.
package assets

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
)

// encodings we know how to serve. Preference between them is not hardcoded:
// Load sorts each file's variants by measured size, because which codec wins
// varies per file and guessing costs real bytes.
var encodings = []string{"zstd", "br", "gzip"}

var encodingExt = map[string]string{
	"zstd": ".zst",
	"br":   ".br",
	"gzip": ".gz",
}

// immutablePrefix holds Astro's content-hashed output (build.assets: '_a').
// Anything under it can be cached forever.
const immutablePrefix = "/_a/"

type representation struct {
	encoding string // "" for identity
	body     []byte
	etag     string
}

type file struct {
	contentType string
	immutable   bool
	identity    representation
	encoded     []representation
}

// Set is an immutable, request-ready view of the built site.
type Set struct {
	files    map[string]*file
	notFound *file
	preload  string // Link header value for HTML responses
}

// Load reads every file from fsys, groups compressed siblings with their
// originals, and precomputes ETags and route aliases.
func Load(fsys fs.FS) (*Set, error) {
	raw := map[string][]byte{}

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == ".gitkeep" {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		raw[p] = b
		return nil
	})
	if err != nil {
		return nil, err
	}

	s := &Set{files: map[string]*file{}}

	for p, body := range raw {
		if isCompressedVariant(p) {
			continue // picked up alongside its origin file
		}

		f := &file{
			contentType: contentTypeFor(p),
			identity:    representation{body: body, etag: etagFor(body, "identity")},
		}

		for _, enc := range encodings {
			if cb, ok := raw[p+encodingExt[enc]]; ok {
				f.encoded = append(f.encoded, representation{
					encoding: enc,
					body:     cb,
					etag:     etagFor(body, enc),
				})
			}
		}

		// Smallest first, so ties in client q-value resolve to fewest bytes.
		sort.Slice(f.encoded, func(i, j int) bool {
			return len(f.encoded[i].body) < len(f.encoded[j].body)
		})

		f.immutable = strings.HasPrefix("/"+p, immutablePrefix)

		for _, route := range routesFor(p) {
			s.files[route] = f
		}
	}

	// Astro emits 404.html at the root; the directory form is there in case the
	// build format ever changes.
	for _, route := range []string{"/404.html", "/404"} {
		if f, ok := s.files[route]; ok {
			s.notFound = f
			break
		}
	}
	s.preload = buildPreloadHeader(s.files)
	return s, nil
}

// isCompressedVariant reports whether p is a sibling like "index.html.br".
func isCompressedVariant(p string) bool {
	for _, ext := range encodingExt {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

// routesFor maps a build output path to every URL that should serve it.
// Astro's directory output means "about/index.html" answers /about and /about/.
func routesFor(p string) []string {
	url := "/" + p

	if !strings.HasSuffix(url, "/index.html") {
		return []string{url}
	}

	dir := strings.TrimSuffix(url, "index.html") // ".../"
	if dir == "/" {
		return []string{"/", url}
	}
	return []string{strings.TrimSuffix(dir, "/"), dir, url}
}

func contentTypeFor(p string) string {
	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// etagFor hashes the identity bytes but varies by encoding, because each
// encoding is a distinct representation and must not share a validator.
func etagFor(identity []byte, encoding string) string {
	sum := sha256.Sum256(identity)
	return `"` + hex.EncodeToString(sum[:8]) + "-" + encoding + `"`
}

// buildPreloadHeader emits preload hints for render-critical subresources.
// Cloudflare turns these Link headers into a 103 Early Hints response.
// Stylesheets are inlined by Astro, so fonts are what is left worth hinting.
func buildPreloadHeader(files map[string]*file) string {
	var fonts []string
	for route := range files {
		if strings.HasSuffix(route, ".woff2") {
			fonts = append(fonts, route)
		}
	}
	sort.Strings(fonts)

	var parts []string
	for _, f := range fonts {
		parts = append(parts, "<"+f+`>; rel=preload; as=font; type="font/woff2"; crossorigin`)
	}
	return strings.Join(parts, ", ")
}

// negotiate picks the best available representation for the request.
//
// Ranking is q-value first (the client's explicit wishes win), then smallest
// body. Identity is always acceptable but sorts last at equal q, so a bare
// "*" still gets a compressed response.
func (f *file) negotiate(header string) representation {
	accepted := parseAcceptEncoding(header)
	if accepted == nil {
		return f.identity
	}

	type candidate struct {
		rep representation
		q   float64
	}

	qFor := func(name string) (float64, bool) {
		if q, ok := accepted[name]; ok {
			return q, true
		}
		if q, ok := accepted["*"]; ok {
			return q, true
		}
		return 0, false
	}

	// f.encoded is already sorted smallest-first, and identity is appended
	// last, so a stable sort by q alone preserves the size tiebreak.
	var candidates []candidate
	for _, rep := range f.encoded {
		if q, ok := qFor(rep.encoding); ok && q > 0 {
			candidates = append(candidates, candidate{rep, q})
		}
	}
	if q, ok := qFor("identity"); !ok || q > 0 {
		// An absent identity entry means acceptable by default (RFC 9110).
		if !ok {
			q = 0.001
		}
		candidates = append(candidates, candidate{f.identity, q})
	}

	if len(candidates) == 0 {
		return f.identity
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].q > candidates[j].q
	})
	return candidates[0].rep
}

// parseAcceptEncoding returns encoding -> qvalue. A q of 0 means "refused".
func parseAcceptEncoding(header string) map[string]float64 {
	if header == "" {
		return nil
	}
	out := map[string]float64{}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, params, _ := strings.Cut(part, ";")
		q := 1.0
		if params != "" {
			if _, qs, ok := strings.Cut(params, "q="); ok {
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(qs), 64); err == nil {
					q = parsed
				}
			}
		}
		out[strings.ToLower(strings.TrimSpace(name))] = q
	}
	return out
}

// Handler serves the set. Only GET and HEAD are answered.
func (s *Set) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		route := path.Clean(r.URL.Path)
		if route != "/" && strings.HasSuffix(r.URL.Path, "/") {
			route += "/"
		}

		f, ok := s.files[route]
		status := http.StatusOK
		if !ok {
			if s.notFound == nil {
				http.NotFound(w, r)
				return
			}
			f, status = s.notFound, http.StatusNotFound
		}

		rep := f.negotiate(r.Header.Get("Accept-Encoding"))
		h := w.Header()

		h.Set("Content-Type", f.contentType)
		h.Set("Vary", "Accept-Encoding")
		h.Set("ETag", rep.etag)

		if rep.encoding != "" {
			h.Set("Content-Encoding", rep.encoding)
		}

		if f.immutable {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// Browsers always revalidate; Cloudflare holds it until CI purges
			// on deploy. That keeps HTML edge-fast without staleness.
			h.Set("Cache-Control", "public, max-age=0, s-maxage=31536000, must-revalidate")
		}

		if strings.HasPrefix(f.contentType, "text/html") && s.preload != "" {
			h.Set("Link", s.preload)
		}

		if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, rep.etag) {
			h.Del("Content-Type")
			h.Del("Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return
		}

		h.Set("Content-Length", strconv.Itoa(len(rep.body)))
		w.WriteHeader(status)

		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(rep.body)
	})
}

// etagMatches handles the comma-separated If-None-Match list and "*".
func etagMatches(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == etag {
			return true
		}
	}
	return false
}

// Routes returns every servable URL, for logging and smoke tests.
func (s *Set) Routes() []string {
	out := make([]string, 0, len(s.files))
	for r := range s.files {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}
