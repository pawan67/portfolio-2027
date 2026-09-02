SITE_URL     ?= http://localhost:8080
# Set to the grey-clouded origin hostname to enable the /perf edge-vs-origin
# comparison. Empty locally, so that control stays hidden.
PUBLIC_ORIGIN_URL ?=
IMAGE        ?= ghcr.io/pawan67/portfolio-2027
COMMIT       := $(shell git rev-parse --verify --quiet HEAD || echo dev)
BUILT_AT     := $(shell date +%s)
PKG          := github.com/pawan67/portfolio-2027/server/internal/buildinfo
COMPRESSIBLE := -name '*.html' -o -name '*.css' -o -name '*.js' -o -name '*.mjs' \
                -o -name '*.svg' -o -name '*.xml' -o -name '*.json' -o -name '*.txt'

.PHONY: help dev build site embed server test lint image run clean

help:
	@grep -E '^[a-z-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

dev: ## Astro dev server with HMR
	cd web && pnpm dev

site: ## Build the static site
	cd web && SITE_URL=$(SITE_URL) COMMIT=$(COMMIT) BUILT_AT=$(BUILT_AT) \
	  PUBLIC_ORIGIN_URL=$(PUBLIC_ORIGIN_URL) pnpm build

# `find -exec` rather than `-print0 | while read -d ''`: `read -d` is a bash
# extension, and make runs recipes under /bin/sh, which is dash on Ubuntu. There
# the loop failed while the success message still printed, so CI built an
# uncompressed site and reported that it had compressed one.
embed: site ## Copy + pre-compress the site into server/dist
	# .gitkeep is tracked so `go build` works in a fresh clone, where dist is
	# otherwise empty and //go:embed all:dist has nothing to match. Recreate it
	# here or every local build shows up as deleting a tracked file.
	rm -rf server/dist && mkdir -p server/dist && touch server/dist/.gitkeep
	cp -R web/dist/. server/dist/
	@if command -v brotli >/dev/null && command -v zstd >/dev/null; then \
	  find server/dist -type f \( $(COMPRESSIBLE) \) -size +512c \
	    -exec sh -c 'for f in "$$@"; do \
	        brotli -q 11 -c "$$f" > "$$f.br"; \
	        zstd -19 -q -c "$$f" > "$$f.zst"; \
	        gzip -9 -c "$$f" > "$$f.gz"; \
	      done' _ {} +; \
	  echo "pre-compressed $$(find server/dist -name '*.br' | wc -l) files as br/zstd/gzip"; \
	else \
	  echo "brotli/zstd not installed - serving identity only (Docker build still compresses)"; \
	fi

build: embed ## Build the server binary with the site embedded
	cd server && CGO_ENABLED=0 go build -trimpath \
	  -ldflags="-s -w -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuiltAt=$(BUILT_AT)" \
	  -o ../bin/server .
	@echo "binary: $$(du -h bin/server | cut -f1)"

run: build ## Build and serve on :8080
	./bin/server

budget: site ## Check the performance budget against the built site
	python3 scripts/check-budget.py web/dist

audit: ## Audit a running deployment (make audit URL=https://example.com)
	python3 scripts/audit.py $(or $(URL),http://localhost:8080)

test: ## Run Go tests
	cd server && go test ./...

lint: ## Vet + format check
	cd server && gofmt -l . && go vet ./...

image: ## Build the container image
	docker build \
	  --build-arg SITE_URL=$(SITE_URL) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILT_AT=$(BUILT_AT) \
	  -t $(IMAGE):$(COMMIT) -t $(IMAGE):latest .

clean:
	rm -rf bin server/dist web/dist web/.astro
