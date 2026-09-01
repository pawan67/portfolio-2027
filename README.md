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
| Site | Astro 7, `output: 'static'`, no UI framework |
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
- **Drains on SIGTERM**, so Swarm's `start-first` rolling update drops nothing,
  and checkpoints field data on the way out.
- **Collects and publishes its own field data** — see below.

## Field measurement

`/perf` publishes what real visitors experience, not a Lighthouse screenshot
taken on a good day.

A deferred module script reports LCP, INP, CLS, TTFB and FCP via `sendBeacon` to
`/api/rum`. The origin folds each value into a fixed-bucket histogram and throws
the value away. There is no database, no per-visitor row, no cookie, no
identifier, and no third party. Country comes from Cloudflare's edge header; the
IP address is never stored.

Histograms rather than raw samples is how CrUX reports Core Web Vitals, and it
means a percentile query is constant time and the whole 28-day window is a few
kilobytes of JSON. Buckets are geometrically spaced so relative error stays
roughly constant — a 50ms error matters at 200ms and is irrelevant at 8s.

Two consequences worth stating plainly:

- **Percentiles are estimates.** Interpolating inside a bucket puts them within
  about one bucket width of the true value. Verified against a known uniform
  distribution in `internal/rum`: true p75 1750ms, reported 1748ms.
- **Prerendered pages are excluded** until activated. Links prerender on hover,
  and counting a prerender nobody looked at would flatter the numbers.

`/perf` also measures this origin against the edge, live, from the visitor's own
browser — a CDN makes any site look fast, which makes most published numbers
ambiguous. That needs a grey-clouded origin hostname to be configured; until then
the control says so rather than showing half a comparison.

## The live origin panel

`/perf` streams the serving machine's own state over Server-Sent Events: CPU,
memory, load average, host uptime, request rate, process heap and goroutine
count, sampled every two seconds.

Beacon uses gopsutil for this. Here the values are read straight out of `/proc`
instead — the target is a Linux VPS, the numbers are identical, and it keeps the
server dependency-free. Docker does not namespace `/proc`, so inside the
container these are the host machine's real figures rather than the container's.

Details that matter more than the feature:

- **Sampling is centralised, not per-connection.** CPU percent is a delta between
  two `/proc/stat` reads; sampling per listener would give every client a
  different and wrong answer.
- **The priming read is never published.** The first `/proc/stat` read has nothing
  to diff against, so publishing it would show a confident `0%`. The panel holds
  its skeleton for one interval instead.
- **A slow listener cannot stall the sampler.** Sends are non-blocking and drop
  that tick for that client; the next one is two seconds away.
- **Write deadlines are extended per frame** via `http.ResponseController`.
  Without it the server's 20s `WriteTimeout` would kill every stream.
- **Heartbeat comments every 30s** keep the connection alive through Cloudflare,
  which drops idle proxied connections at around 100 seconds.
- **Listeners are capped**, and the handler returns `503` with `Retry-After` past
  the cap rather than degrading the stream for everyone.

Disk usage is off by default. `statfs` inside the container measures the overlay
filesystem, and reporting the host's disk means bind-mounting a host path into an
internet-facing container — a real trade, so it is opt-in via `METRICS_DISK_PATH`
rather than quietly enabled.

## Performance budget

Enforced in CI by `scripts/check-budget.sh`; the build fails when a budget is blown.

| Budget | Limit | Why |
|---|---|---|
| HTML, brotli | 14 KB | fits the initial congestion window — one round trip |
| CSS per page, brotli | 6 KB | inlined, so it is paid on every document |
| CSS per page, raw | 16 KB | guards parse cost and unbounded growth |
| Render-blocking JS | 0 bytes | nothing stands between the HTML and first paint |
| Deferred JS per page, brotli | 4 KB | measurement is not free, but it is bounded |
| Deferred JS on `/perf`, brotli | 8 KB | stated override — that page is an instrument |

`/perf` holds an SSE connection, fetches and renders field data, and runs the
edge-versus-origin control, so it carries about 1.6 KB more than a content page.
The override is declared in `scripts/check-budget.py` and printed on every run,
so an exception cannot quietly become the norm.

The JS budget was originally "zero bytes, full stop". Collecting field data makes
that impossible — you cannot measure a real visitor without running code in their
browser. Rather than quietly drop the budget, it was split: nothing may block
rendering, and what does ship stays bounded and is checked per page.

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
webhook, purges the Cloudflare edge, polls `/api/build` until the live commit
matches the one it just pushed, then audits production. A green deploy means the
bytes being served are the ones just built *and* that they are being served
correctly — not merely that the push succeeded.

Full setup, including the Cloudflare settings that will silently break the
hash-based CSP if left on, is in [docs/DEPLOY.md](docs/DEPLOY.md).

## Verifying a deployment

```sh
make audit URL=https://example.com
```

`scripts/audit.py` checks a running deployment against what this repository claims
about it: encoding negotiation and that the smallest variant wins, `ETag` varying
by encoding, conditional requests, immutable caching on hashed assets and fonts
but not HTML, security headers, a hash-based CSP with no `unsafe-inline`,
speculation rules that actually parse, no render-blocking scripts, every API
endpoint, and — against production — TLS, HTTP/2 or HTTP/3, 103 Early Hints, and
the HTTPS redirect.

It is read-only, so it is safe against a host shared with other services. Checks
that cannot apply are skipped rather than quietly passed. It runs in CI against
the real binary on every push, and against production after every deploy.

There is deliberately no Terraform and no Ansible hardening playbook: this VPS
already existed and already runs other services, so tooling that assumes it owns
the machine is the wrong shape. The reasoning is written out in
[docs/DEPLOY.md](docs/DEPLOY.md#why-there-is-no-terraform).

## Known gaps

- **Dokploy's own project config lives in its database, not in git.** The service
  definition is in `infra/compose/`, and Terraform/Ansible will own everything
  below Dokploy, but creating the Dokploy project itself is a manual step. This is
  not fully reproducible infrastructure and is not described as such.
- **`BuildSeconds` is wired but unset.** A build cannot bake in how long it took to
  build. Replacing it with an authenticated post-rollout ingest that measures real
  end-to-end deploy latency is still outstanding.
- **One replica, deliberately.** The field-data checkpoint is a single file on a
  shared volume; two replicas would keep separate in-memory histograms and
  overwrite each other on flush. Zero-downtime does not depend on replica count —
  `start-first` provides it. The one window where two tasks coexist is a rolling
  deploy, which at a 60s flush interval can drop a handful of samples. That is
  immaterial to a 28-day percentile, but it is not nothing.
- **Astro does not evaluate `{}` inside `<script>`.** The speculation rules are
  written as literal JSON with `is:inline` for this reason. Interpolating a
  frontmatter variable emits the source text verbatim and the rules silently fail
  to parse — it looks correct in the editor and does nothing in the browser.
- **HSTS omits `preload`.** Submitting to the preload list is effectively
  irreversible; opt in deliberately once every subdomain is HTTPS.
- **Font preload hints currently list every `.woff2`.** Narrow this to the single
  display face when fonts land, or the hints compete for bandwidth.
