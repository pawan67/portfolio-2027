---
title: Beacon
summary: A homelab dashboard that ships as one 14MB Go binary with the React frontend compiled in.
year: 2026
stack: ["Go", "React", "embed.FS", "Docker"]
role: "Design and engineering"
order: 1
---

Self-hosted dashboards tend to force a trade. Glance and Homarr are capable but
plain; Heimdall is tidy but rigid. The ones that look good are hard to configure,
and the ones that configure well look like they were built in 2014.

Beacon is an attempt to refuse the trade: a lean Go backend, a genuinely modern
React frontend, and a widget framework built for configuration rather than
around it.

## One binary, no runtime

The entire frontend is compiled into the Go binary through `embed.FS`. Deploying
Beacon means copying a single ~14MB file — no Node process, no static file
server, no reverse proxy required, no `node_modules` on the host. The container
image is about 20MB and idles on very little memory.

This is the property that matters most on a homelab box, where the dashboard is
supposed to be the least demanding thing running.

## Configuration that survives being edited twice

Most self-hosted tools make you choose: edit a config file, or use the UI. Pick
the file and the UI is read-only. Pick the UI and your configuration lives in an
opaque database.

Beacon does both, in both directions. Every widget declares a schema, and its
settings form is generated from that schema — so a new widget gets a working
editor for free. UI edits round-trip back into a human-readable `config.yaml`
with comments and key ordering preserved, and a file watcher live-reloads any
open tab when the file changes on disk.

The result is that YAML and the UI stay honest about each other. You can hand-edit
in an editor, then keep tweaking in the browser, and neither clobbers the other.

## Widgets

| Widget | Server data | Notes |
|---|---|---|
| Clock | — | 12/24h, optional date |
| Search | — | Any engine via a `{query}` template |
| Bookmarks | — | Grouped links with favicons |
| Service Tiles | yes | Live HTTP/TCP health checks |
| System | yes | CPU, memory, disk, uptime |
| Docker | yes | Container list and status, read-only socket |
| Weather | yes | Open-Meteo, no API key |
| RSS | yes | Multi-feed, recency-sorted |
| Custom API | yes | Any JSON endpoint as a stat, list, or table |

The Custom API widget is the escape hatch that keeps the widget list short. Point
it at any JSON endpoint and render the result as a stat, a list, or a table — no
plugin system, no build step, no new widget to write for most one-off cases.

## Why it is on this site

The system collectors Beacon uses to report CPU, memory, disk and container state
are the same ones feeding the live panel on this site. This portfolio is, in a
small way, a Beacon deployment wearing different clothes.
