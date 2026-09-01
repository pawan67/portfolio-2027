# syntax=docker/dockerfile:1

# ---------------------------------------------------------------------------
# 1. Build the static site.
# ---------------------------------------------------------------------------
FROM node:22-alpine AS web
RUN npm install -g pnpm@10.32.1
WORKDIR /app
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
ARG SITE_URL=https://example.com
ENV SITE_URL=${SITE_URL}
RUN pnpm build

# ---------------------------------------------------------------------------
# 2. Pre-compress every text asset at maximum effort.
#
# Doing this once at build time means the origin never spends CPU compressing
# a response, and we can afford brotli 11 / zstd 19, which are far too slow to
# run per-request.
# ---------------------------------------------------------------------------
FROM alpine:3.22 AS compress
RUN apk add --no-cache brotli zstd gzip findutils
COPY --from=web /app/dist /dist
RUN find /dist -type f \
      \( -name '*.html' -o -name '*.css' -o -name '*.js' -o -name '*.mjs' \
         -o -name '*.svg' -o -name '*.xml' -o -name '*.json' -o -name '*.txt' \
         -o -name '*.webmanifest' \) \
      -size +512c -print0 \
    | while IFS= read -r -d '' f; do \
        brotli -q 11 -c "$f" > "$f.br"; \
        zstd -19 -q -c "$f" > "$f.zst"; \
        gzip -9 -c "$f" > "$f.gz"; \
      done

# ---------------------------------------------------------------------------
# 3. Compile the origin with the site embedded.
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src
# The bracket glob makes go.sum optional while the server has no dependencies.
COPY server/go.mod server/go.su[m] ./
RUN go mod download
COPY server/ ./
COPY --from=compress /dist ./dist

ARG COMMIT=dev
ARG BUILT_AT=0
ARG BUILD_SECONDS=0
ENV PKG=github.com/pawan67/portfolio-2027/server/internal/buildinfo
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags="-s -w \
        -X ${PKG}.Commit=${COMMIT} \
        -X ${PKG}.BuiltAt=${BUILT_AT} \
        -X ${PKG}.BuildSeconds=${BUILD_SECONDS}" \
      -o /server .

# ---------------------------------------------------------------------------
# 4. Runtime: one static binary, nothing else.
# ---------------------------------------------------------------------------
FROM scratch
COPY --from=build /server /server

USER 65534:65534
EXPOSE 8080
ENV PORT=8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=2s --retries=3 \
  CMD ["/server", "-healthcheck"]

ENTRYPOINT ["/server"]
