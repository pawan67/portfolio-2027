---
title: This site
summary: A static build compiled into a Go binary on a scratch container, arguing for itself with published telemetry rather than adjectives.
year: 2026
stack: ["Go", "Astro", "Docker", "Dokploy", "Cloudflare"]
role: "Design and engineering"
repo: "https://github.com/pawan67/portfolio-2027"
order: 5
---

Every portfolio claims to be fast. This one publishes field Core Web Vitals from
real visitors, the state of the machine it runs on, and how long its last deploy
took, and lets you decide.

```
Cloudflare (TLS, HTTP/3, 103 Early Hints, edge-cached HTML)
  └─ Traefik (Dokploy ingress, Let's Encrypt)
       └─ Go binary  ← the whole site lives in embed.FS
```

The container is `FROM scratch` and holds one static binary with the built site
compiled into it. No node process, no runtime filesystem access, no dependency
outside the Go standard library. There is no `curl` in the image for Docker's
healthcheck to call, so the binary probes itself.

## What the origin does that a file server does not

Brotli, zstd and gzip bodies are generated at image build time at compression
levels far too slow to run per request, so the origin spends no CPU compressing
and still serves the small version.

Which codec actually wins varies per file, so the origin ranks the encodings a
client accepts by their measured size rather than by a hardcoded preference
order — on the landing page brotli beats zstd by 16%, and a static ranking would
have given those bytes away. Client `q` values still outrank size when they are
explicit, because that is what they are for.

Each encoding also gets its own ETag. They are distinct representations, and
sharing one validator between them lets a cache hand a brotli body to a client
that asked for identity.

## Measuring instead of asserting

The measurements page reports p75 over a 28-day window, which is not an
arbitrary choice — it matches CrUX's window, so the number here is comparable to
what an external tool reports for the same site rather than being a private
scale.

The live panel is a server-sent stream of the origin's own machine state,
sampled every two seconds. Listeners are held-open connections, so they are
capped, and the handler answers past that cap with a `503` and a `Retry-After`
instead of degrading the panel for everyone already watching.

The site itself ships no JavaScript to the page you are reading. The telemetry
that collects these numbers is the only script on it.
