# syntax=docker/dockerfile:1

# Built for more than one architecture: CI runs on x86_64 while the deployment
# target is an arm64 VPS, and an amd64 binary there fails with the unhelpful
# "exec format error". Every builder stage is pinned to $BUILDPLATFORM and the
# Go build cross-compiles to $TARGETARCH, so multi-arch costs a second link
# rather than a full emulated build.

# ---------------------------------------------------------------------------
# 1. Build the static site.
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
RUN npm install -g pnpm@10.32.1
WORKDIR /app
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
ARG SITE_URL=https://example.com
ARG COMMIT=dev
ARG BUILT_AT=0
# Enables the bare-origin comparison on /perf. Unset simply hides that control.
ARG PUBLIC_ORIGIN_URL=
ENV SITE_URL=${SITE_URL} COMMIT=${COMMIT} BUILT_AT=${BUILT_AT} \
    PUBLIC_ORIGIN_URL=${PUBLIC_ORIGIN_URL}
RUN pnpm build

# ---------------------------------------------------------------------------
# 2. Pre-compress every text asset at maximum effort.
#
# Doing this once at build time means the origin never spends CPU compressing
# a response, and we can afford brotli 11 / zstd 19, which are far too slow to
# run per-request.
# ---------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM alpine:3.22 AS compress
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
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
# The bracket glob makes go.sum optional while the server has no dependencies.
COPY server/go.mod server/go.su[m] ./
RUN go mod download
COPY server/ ./
COPY --from=compress /dist ./dist

# scratch has no shell, so the data directory has to be created here and
# copied in with ownership already set.
RUN mkdir -p /data && chown 65534:65534 /data

ARG COMMIT=dev
ARG BUILT_AT=0
ARG BUILD_SECONDS=0
# Set by buildx per target platform. Go cross-compiles natively, so the whole
# build runs at the builder's own speed rather than under QEMU emulation.
ARG TARGETARCH
ENV PKG=github.com/pawan67/portfolio-2027/server/internal/buildinfo
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath \
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
COPY --from=build --chown=65534:65534 /data /data

USER 65534:65534
EXPOSE 8080
ENV PORT=8080 RUM_DATA=/data/rum.json
VOLUME ["/data"]

HEALTHCHECK --interval=10s --timeout=3s --start-period=2s --retries=3 \
  CMD ["/server", "-healthcheck"]

ENTRYPOINT ["/server"]
