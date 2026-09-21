---
title: Lift
summary: A local-first workout tracker where the account is optional, and the sync server it would talk to is in the same repository.
year: 2026
stack: ["Expo", "React Native", "NestJS", "Postgres", "TypeScript"]
role: "Design and engineering"
repo: "https://github.com/pawan67/lift"
live: "https://lift.pawan67.dev/"
order: 1
---

Fitness apps ask you to trust a company with several years of training history
before you have logged a single set. Lift inverts that. Every workout, personal
record and body-weight entry writes to a database on the device first. There is
no account step, no server call and no connectivity requirement in the path
between opening the app and finishing a session.

Sync exists, but it is opt-in, and the API behind it — NestJS and Postgres — is
in this repository with a Compose file next to it. If you want the backup and
the second device, you can run the thing that provides them.

## The parts that only matter on a real phone

A rest timer that stops when the OS decides your app is not important is not a
rest timer. Lift's is backed by an Android foreground service, so the countdown
in the notification shade stays live even when the app is killed mid-set. The
bell at zero rings on the notification or alarm stream rather than the media
stream, which is the difference between hearing it and not when there is music
playing through a pair of earbuds on the bench.

The same category of problem produced the most interesting bug in the project.
React Native's global `fetch` is implemented on XMLHttpRequest, and an
XMLHttpRequest response has no `body`. A streaming request made with it does
not throw — it waits for the entire answer and hands it over at once. The
feature looks finished, feels broken, and review cannot see it. The client uses
`fetch` from `expo/fetch` instead. Relatedly, SSE frames do not align with
network chunks, so the line buffering sits in `packages/shared` as a decoder
tested at every chunk size from one byte upward, rather than inside a fetch loop
where it could not be tested without a socket.

## Logic owns the numbers, the model owns the prose

The AI coach is off until you add your own key, and the screens it appears on
are useful without one: the weekly set count per muscle is compared against
volume landmarks in `packages/shared`, which is arithmetic over data already on
the device, unit tested, and works on a plane.

What a key buys is the sentences. The rule the whole feature is built on is that
a model never produces a figure — it is handed the numbers and asked where to
put the sets. A model that arrives at a slightly different set count is not a
second opinion; it is a contradiction of the screen next to it that the reader
has no way to adjudicate.

The key goes into the OS keychain through `expo-secure-store`, not into
settings, which means it is not in a backup and is never rendered back into a
field. Requests go from the phone straight to the provider, never through a Lift
server. Pointing the base URL at Ollama keeps the entire thing on your own
machine.

## Shipping without a store

Releases are APKs cut by GitHub Actions, with `expo-updates` delivering
JavaScript fixes to installed builds so most changes reach a phone without a
download-and-install prompt. `runtimeVersion` is on the fingerprint policy — a
hash of everything that shapes the native app — so a build only accepts updates
carrying its own fingerprint. That is what stops a JavaScript bundle reaching a
build with no native module to back it, and it is enforced rather than
remembered.

Six files carry the version number and CI fails if they disagree, because the
alternative is a release where the tag, the APK and the settings screen each
claim something different.
