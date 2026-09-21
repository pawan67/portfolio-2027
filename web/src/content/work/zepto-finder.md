---
title: Zepto Finder
summary: Finds which nearby dark stores actually hold an item, then ships the same engine a second time to run entirely on the phone.
year: 2026
stack: ["FastAPI", "React", "SQLite", "Capacitor", "TypeScript"]
role: "Design and engineering"
repo: "https://github.com/pawan67/zepto-stock-checker"
order: 3
---

Quick-commerce stock is not a property of your pincode. It is a property of each
dark store, and the app will only ever tell you about the one it has already
assigned you. So "is this in stock near me" is a question the official interface
structurally cannot answer.

Paste a shared product link, set a location and a radius, and this answers it: a
map and a distance-sorted list of the stores that have the thing, with the price
at each one.

## Discovering stores that are not listed anywhere

There is no endpoint that returns the dark stores near a point, so the radius is
swept with serviceability probes — ask whether a coordinate can be served, and
the answer reveals which store would serve it. Everything discovered is cached
in SQLite, which gives the system a sharp and deliberate performance asymmetry:
the first search in a new area is slow, because a 50 km sweep is a few hundred
probes, and every later search in that area finishes in seconds.

Probe count grows with area rather than distance, so the radius cap is the
single biggest lever on what a search costs — roughly 253 probes at 25 km
against 37 at 10 km. It is configurable for that reason, and results stream back
over SSE so a cold sweep shows its work instead of spinning.

## The same logic twice, on purpose

The web version runs the engine on a server, which makes that server's IP the
thing standing between every user and the upstream API — a single address to get
blocked, and a bill that grows with other people's searches.

The Android build removes it. The Zepto logic is ported from Python to
TypeScript and runs on the device, calling out through a small native OkHttp
plugin that sidesteps browser CORS and can read the `Set-Cookie` headers the
store sweep depends on. The same React interface is reused through Capacitor,
and a runtime check picks the backend or the on-device engine. Every request now
comes from the user's own phone and IP: nothing central to host, nothing to get
blocked, and it scales to everyone for free.

Two implementations of the same rules is exactly the kind of thing that drifts,
so the on-device engine carries a Vitest suite that ports the backend's pytest
tests one to one.

## The obvious caveat

This calls an internal API that was never published, and the README says so
rather than burying it. It can break without notice, there is a smoke script for
diagnosing that, and it is meant to stay personal and low-volume.
