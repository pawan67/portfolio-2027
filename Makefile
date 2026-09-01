SITE_URL     ?= http://localhost:8080
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
	cd web && SITE_URL=$(SITE_URL) COMMIT=$(COMMIT) BUILT_AT=$(BUILT_AT) pnpm build

embed: site ## Copy + pre-compress the site into server/dist
	rm -rf server/dist && mkdir -p server/dist
	cp -R web/dist/. server/dist/
	@if command -v brotli >/dev/null && command -v zstd >/dev/null; then \
	  find server/dist -type f \( $(COMPRESSIBLE) \) -size +512c -print0 \
	  | while IFS= read -r -d '' f; do \
	      brotli -q 11 -c "$$f" > "$$f.br"; \
	      zstd -19 -q -c "$$f" > "$$f.zst"; \
	      gzip -9 -c "$$f" > "$$f.gz"; \
	    done; \
	  echo "pre-compressed br/zstd/gzip"; \
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
