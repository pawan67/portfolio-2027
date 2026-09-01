// Command server is the origin for the portfolio site.
//
// The built Astro output is compiled into the binary, so the container is a
// single static file with no runtime dependencies and no filesystem reads on
// the request path.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/pawan67/portfolio-2027/server/internal/assets"
	"github.com/pawan67/portfolio-2027/server/internal/buildinfo"
	"github.com/pawan67/portfolio-2027/server/internal/rum"
)

// dist is populated by the build: `pnpm build` in web/, then the output plus
// its .br/.zst/.gz siblings are copied to server/dist/.
//
//go:embed all:dist
var distFS embed.FS

const (
	// Matches CrUX's reporting window, so the published p75 is comparable to
	// what external tools report for the same site.
	rumWindowDays = 28
	// How often the in-memory histograms are checkpointed to disk.
	rumFlushInterval = time.Minute
)

func main() {
	// The runtime image is `scratch`, so there is no curl for Docker's
	// HEALTHCHECK to call. The binary probes itself instead.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck())
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return err
	}

	site, err := assets.Load(sub)
	if err != nil {
		return err
	}

	store, err := rum.NewStore(envOr("RUM_DATA", "/data/rum.json"), rumWindowDays)
	if err != nil {
		return err
	}

	info := buildinfo.Get(runtime.Version())
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("GET /api/build", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_ = json.NewEncoder(w).Encode(info)
	})

	mux.Handle("POST /api/rum", rum.Ingest(store, time.Now))
	mux.Handle("GET /api/perf", rum.Serve(store, time.Now))

	// Lets the perf page time this origin directly and compare it against the
	// same request served through Cloudflare.
	ping := pingHandler(envOr("SITE_ORIGIN", ""))
	mux.Handle("GET /api/ping", ping)
	mux.Handle("OPTIONS /api/ping", ping)

	mux.Handle("/", site.Handler())

	addr := ":" + envOr("PORT", "8080")
	srv := &http.Server{
		Addr:              addr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go flushLoop(ctx, log, store)

	// Bind before logging success, so a failed bind never emits "listening".
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	log.Info("listening",
		"addr", addr,
		"commit", info.ShortCommit,
		"routes", len(site.Routes()),
	)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	// Swarm sends SIGTERM before rotating a task out. Drain in-flight requests
	// so a rolling deploy drops nothing, then checkpoint whatever the last
	// interval did not cover.
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	shutdownErr := srv.Shutdown(shutdownCtx)
	if err := store.Save(); err != nil {
		log.Error("final rum checkpoint failed", "err", err)
	}
	return shutdownErr
}

// flushLoop checkpoints field data and drops days that have aged out.
func flushLoop(ctx context.Context, log *slog.Logger, store *rum.Store) {
	ticker := time.NewTicker(rumFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if removed := store.Prune(now); removed > 0 {
				log.Info("pruned rum buckets", "count", removed)
			}
			if err := store.Save(); err != nil {
				log.Error("rum checkpoint failed", "err", err)
			}
		}
	}
}

// pingHandler answers a minimal timing probe. allowOrigin is the site's public
// origin; the perf page is served from there and measures this endpoint on the
// bare-origin hostname, which makes the request cross-origin.
func pingHandler(allowOrigin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if allowOrigin != "" {
			h.Set("Access-Control-Allow-Origin", allowOrigin)
			// Without this, Resource Timing reports zeroes for every phase of a
			// cross-origin request and the comparison would be meaningless.
			h.Set("Timing-Allow-Origin", allowOrigin)
			h.Set("Vary", "Origin")
		}
		h.Set("Cache-Control", "no-store")

		if r.Method == http.MethodOptions {
			h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			h.Set("Access-Control-Max-Age", "86400")
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

// securityHeaders sets the headers a meta CSP cannot express, plus the usual
// hardening. The page-level CSP is emitted by Astro as a hashed <meta> tag.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=(), interest-cohort=()")

		// No `preload` directive: submitting to the preload list is effectively
		// irreversible, so opt in deliberately once every subdomain is HTTPS.
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")

		next.ServeHTTP(w, r)
	})
}

func healthcheck() int {
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get("http://127.0.0.1:" + envOr("PORT", "8080") + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
