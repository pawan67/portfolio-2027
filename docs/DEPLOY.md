# Deploying

This site runs on an existing VPS that already hosts other services, and deploys
through Dokploy. That shapes what is automated and what is not — see
[Why there is no Terraform](#why-there-is-no-terraform).

## One-time setup

### 1. Dokploy project

Create a **Compose** application (not a Dockerfile application) so the service
definition stays in git rather than in Dokploy's database.

- **Provider**: this repository, branch `main`
- **Compose path**: `infra/compose/docker-compose.yml`
- **Environment**:

  | Variable | Example | Purpose |
  |---|---|---|
  | `DOMAIN` | `example.com` | Traefik host rules |
  | `IMAGE` | `ghcr.io/pawan67/portfolio-2027` | image to pull |
  | `TAG` | `latest` | overridden per deploy by CI |

The compose file declares a named volume `rum-data` for the field-data
checkpoint. Do not remove it between deploys or the 28-day window resets.

Copy the deploy webhook URL Dokploy generates; CI needs it below.

### 2. GitHub repository configuration

| Kind | Name | Notes |
|---|---|---|
| variable | `SITE_URL` | `https://example.com` |
| variable | `PUBLIC_ORIGIN_URL` | `https://origin.example.com`, enables the `/perf` comparison |
| secret | `DOKPLOY_WEBHOOK_URL` | from step 1 |
| secret | `CLOUDFLARE_ZONE_ID` | zone overview page |
| secret | `CLOUDFLARE_API_TOKEN` | scoped to *Zone → Cache Purge* only |

The API token needs nothing beyond cache purge. Do not reuse a global key.

### 3. DNS

| Record | Name | Proxy | Why |
|---|---|---|---|
| A | `@` | **Proxied** (orange) | the site itself |
| A | `origin` | **DNS only** (grey) | bare-origin timing comparison |

The grey-clouded record is the whole point of the edge-versus-origin control on
`/perf`: it exposes the VPS directly so the two paths can be timed against each
other. Traefik issues a separate certificate for it, which the compose file
already declares.

### 4. Cloudflare zone settings

These matter more than they look. Several of them will silently undo work the
origin does.

**Turn on**

| Setting | Why |
|---|---|
| SSL/TLS mode: **Full (strict)** | anything less lets the edge talk plaintext to the origin |
| Always Use HTTPS | the audit checks for the redirect |
| HTTP/3 (with QUIC) | |
| Brotli | |
| **Early Hints** | the origin emits `Link: rel=preload`; only Cloudflare can turn that into a 103, because Traefik will not forward a 1xx from the origin |

**Turn off — these rewrite HTML and will break the hash-based CSP**

| Setting | What it breaks |
|---|---|
| Rocket Loader | rewrites and defers scripts; invalidates every script hash |
| Auto Minify (if present on your plan) | rewrites the inlined `<style>`; invalidates the style hash |
| Email Obfuscation | injects script into the document; invalidates hashes |
| Mirage / Polish | rewrites image markup |

A broken CSP does not fail loudly. The page renders and the browser silently
refuses the stylesheet or script. `scripts/audit.py` checks the CSP is
hash-based, and Lighthouse in CI catches the rendering consequences.

**Cache rule**

HTML is served with `s-maxage=31536000, must-revalidate`, so the edge is meant to
hold it until a deploy purges it. Add a cache rule matching the hostname with
*Eligible for cache* and *Use cache-control header*. CI purges on every deploy,
so staleness is bounded by the deploy itself.

## Deploying

Push to `main`. CI will:

1. build the image off the VPS and push it to GHCR
2. call the Dokploy webhook
3. purge the Cloudflare edge
4. poll `/api/build` until the live commit matches the pushed one
5. run `scripts/audit.py` against production

Step 4 is what makes a green job mean something: it fails if the bytes being
served are not the ones just built. Step 5 fails if they are being served wrongly.

## Verifying by hand

```sh
make audit URL=https://example.com
```

Read-only — it issues ordinary GETs and reads the responses. Safe to run against
production, including a host shared with other services.

Against a local server it skips the TLS, HTTP/3, Early Hints and Cloudflare
checks rather than pretending they passed.

## Rolling back

Dokploy redeploys the previous image tag. Swarm's `start-first` policy means the
replacement must pass its healthcheck before the current task is stopped, so a
rollback is as safe as a deploy. Purge the Cloudflare cache afterwards, or the
edge will keep serving the rolled-back HTML for up to a year.

## Why there is no Terraform

Terraform is for infrastructure it owns. This VPS already existed and already runs
other services, so importing it would put Terraform in charge of state it did not
create and cannot fully see — and one careless `apply` reaches those other
services. The same argument rules out an Ansible hardening playbook: rewriting
`sshd_config`, `ufw` and `fail2ban` on a box that is already serving traffic is a
good way to break something unrelated at an inconvenient hour.

What that leaves is worth more here anyway. The deployable unit is declared in
git (`infra/compose/`), the image is reproducible from a commit, and
`scripts/audit.py` verifies the running deployment against what the repository
claims about it. Declaring intent is easy; this checks reality.

If this ever moves to a dedicated host, provisioning it with Terraform becomes
the right call and this section should be deleted.
