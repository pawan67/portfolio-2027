---
title: Instamart Alerts
summary: Watches Instamart for a discount crossing a threshold and sends a Telegram message, with a console that shows why it stayed quiet.
year: 2026
stack: ["Python", "Playwright", "httpx", "Telegram"]
role: "Design and engineering"
repo: "https://github.com/pawan67/instamart-alerts"
live: "https://insta-alerts.000427.xyz/"
order: 4
---

A watcher that polls a search and messages you when something is cheap enough is
a small idea. Almost all of the work is in the two places it quietly goes wrong:
getting past the front door, and reading only part of the answer.

## Getting past the WAF once, then staying cheap

Instamart's web API sits behind an AWS WAF JavaScript challenge, so a plain HTTP
request receives a `202` and a challenge page rather than JSON. Solving that per
poll would mean running a browser per poll.

Instead there is a bootstrap: headless Chromium loads the page once, the
challenge script runs, and the resulting token cookie is cached to disk. Every
poll after that is a plain `httpx` call carrying that cookie, about a second
each. When the token goes stale the API says so with a `202` or `403`, and the
runner re-mints and retries, escalating to a full browser only if it has to.

Prices are per dark store, so a session has to be pinned to one before
searching, which is a four-call chain from area to `place_id` to coordinates to
store. The store id is the awkward part: the endpoint that selects a location
does not return it as a field. It appears only inside the `swiggy://` deeplinks
of the home feed that comes back, so the code takes the most frequent one.

## Page one is not a smaller version of the answer

Search returns roughly 32 products and hides the rest behind a pagination
cursor, which has to travel back alongside a second offset field or the same
first page is served again.

It would be easy to read page one and call it done. Measured at one store, page
one held 73 of 231 variants — and results are ranked by relevance, not by
discount, so the deal worth being woken up for is as likely to sit on page
three. Each watch therefore costs one call per page; a broad query like `yogurt`
runs to four. The walk stops at eight pages and says so in the log when it does,
rather than silently truncating.

## Telling the two silences apart

The control panel is a plain browser page with no Telegram involvement: connect
the bot, edit what you are watching, and read the log live. Adding a watch pulls
real prices so a threshold is set against actual numbers instead of a guess.

The part that earns its keep is the console. Every finished run expands into the
full breakdown — each product the watch kept, best discount first, the ones over
the threshold picked out, the ones actually sent flagged. That is the only way
to distinguish "nothing was on offer today" from "my threshold has been too high
for a month", which are identical from the outside and mean opposite things.
