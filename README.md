# portfolio-2027

A portfolio whose argument is its own telemetry: it publishes real numbers about
itself — field Core Web Vitals, origin machine state, deploy latency — instead of
claiming to be fast.

## Architecture

```
Cloudflare (TLS, HTTP/3, 103 Early Hints, edge-cached HTML)
  └─ Traefik (Dokploy ingress, Let's Encrypt)
       └─ Go binary  ← the whole site lives in embed.FS
```

The container is `FROM scratch` and holds one static binary. There is no runtime
filesystem access, no node process, and no dependency beyond the Go standard
library.

| Layer | Choice |
|---|---|
| Site | Astro 7, `output: 'static'`, React islands only where interactive |
| Styling | Tailwind v4, inlined into every document |
| Origin | Go 1.26, stdlib only |
| Platform | Dokploy in Compose mode (definition lives in `infra/compose/`) |
| CI | GitHub Actions → GHCR → Dokploy webhook → Cloudflare purge |

## What the origin does that a file server does not

- **Serves pre-compressed bytes.** brotli 11, zstd 19 and gzip 9 are generated at
  image build time, so the origin spends zero CPU compressing and can afford
  compression levels far too slow to run per request.
- **Ranks encodings by measured size, not by reputation.** Which codec wins varies
  per file. On the current landing page brotli beats zstd by 16%; a hardcoded
  preference order gave those bytes away.
- **Varies the ETag by encoding**, because each encoding is a distinct
  representation and sharing a validator lets a cache hand a brotli body to a
  client that asked for identity.
- **Emits `Link: rel=preload` headers** on HTML, which Cloudflare converts into a
  103 Early Hints response. Traefik will not forward a 1xx from the origin, so the
  edge has to be the one to send it.
- **Probes itself** for Docker's healthcheck, since `scratch` has no shell.
- **Drains on SIGTERM**, so Swarm's `start-first` rolling update drops nothing.

## Performance budget

Enforced in CI by `scripts/check-budget.sh`; the build fails when a budget is blown.

| Budget | Limit | Why |
|---|---|---|
| HTML, brotli | 14 KB | fits the initial congestion window — one round trip |
| CSS per page, brotli | 6 KB | inlined, so it is paid on every document |
| CSS per page, raw | 16 KB | guards parse cost and unbounded growth |
| JS on the landing page | 0 bytes | islands are opt-in and deferred |

The CSS budget started at 10 KB uncompressed and was revised upward once the
design system existed: Tailwind's preflight is ~4.3 KB and the theme layer
~1.2 KB before a single utility is emitted, so 10 KB was below the floor. The
compressed figure is the tighter and more meaningful guard — it is what
visitors actually pay.

## Typography

One display face, [Instrument Serif](https://fonts.google.com/specimen/Instrument+Serif),
latin subset, 21 KB, preloaded. Body text uses the system sans stack and costs
nothing.

The fallback is metric-matched from measured values rather than estimated, so
the swap when the webfont lands does not move anything:

| | Instrument Serif | Times New Roman |
|---|---|---|
| avg lowercase advance | 0.3922 em | 0.4593 em |
| unitsPerEm | 1000 | — |
| ascent / descent | 990 / −310 | — |

That gives `size-adjust: 85.39%`, `ascent-override: 99%`, `descent-override: 31%`.
Times metrics were read from Liberation Serif, which is metric-compatible with it.

Navigation is instant without a SPA: a declarative `<script type="speculationrules">`
block prerenders on hover (parsed, never executed) and native CSS
`@view-transition` animates the swap. Both cost zero JavaScript.

## Local development

```sh
make dev      # Astro dev server with HMR
make build    # static site → pre-compressed → embedded → ./bin/server
make run      # build and serve on :8080
make test     # Go tests
make image    # container image
```

`make build` degrades gracefully if `brotli`/`zstd` are not installed locally; the
Docker build always compresses.

## Deploying

Push to `main`. CI builds the image off the VPS, pushes to GHCR, calls the Dokploy
webhook, purges the Cloudflare edge, then polls `/api/build` until the live commit
matches the one it just pushed — so a green deploy job means the bytes are actually
being served, not merely that the push succeeded.

Required repository configuration:

| Kind | Name |
|---|---|
| variable | `SITE_URL` |
| secret | `DOKPLOY_WEBHOOK_URL` |
| secret | `CLOUDFLARE_ZONE_ID` |
| secret | `CLOUDFLARE_API_TOKEN` |

## Known gaps

- **Dokploy's own project config lives in its database, not in git.** The service
  definition is in `infra/compose/`, and Terraform/Ansible will own everything
  below Dokploy, but creating the Dokploy project itself is a manual step. This is
  not fully reproducible infrastructure and is not described as such.
- **`BuildSeconds` is wired but unset.** A build cannot bake in how long it took to
  build. It is replaced in Phase 3 by an authenticated ingest that CI calls after
  rollout, which measures real end-to-end deploy latency — a better number anyway.
- **HSTS omits `preload`.** Submitting to the preload list is effectively
  irreversible; opt in deliberately once every subdomain is HTTPS.
- **Font preload hints currently list every `.woff2`.** Narrow this to the single
  display face when fonts land, or the hints compete for bandwidth.
