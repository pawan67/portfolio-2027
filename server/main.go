// Command server is the origin for the portfolio site.
//
// The built Astro output is compiled into the binary, so the container is a
// single static file with no runtime dependencies and no filesystem reads.
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
)

// dist is populated by the build: `pnpm build` in web/, then the output plus
// its .br/.zst/.gz siblings are copied to server/dist/.
//
//go:embed all:dist
var distFS embed.FS

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
	// so a rolling deploy drops nothing.
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
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
