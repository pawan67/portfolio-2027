---
title: Portfolio OS
summary: A desktop environment in the browser — draggable windows, a dock, and a theme derived from the wallpaper.
year: 2025
stack: ["Next.js", "React 19", "Zustand", "Framer Motion"]
role: "Design and engineering"
order: 2
---

<!-- TODO(pawan): the stack below is accurate, but the narrative is a sketch.
     Replace the two sections marked with a TODO with what actually happened:
     what was hard, what you threw away, what you would do differently. -->

A portfolio shaped like an operating system: draggable, resizable windows, a
persistent dock, and per-app state that survives being closed and reopened.

## Windowing

Window geometry and z-order live in a single Zustand store, which keeps drag,
resize, focus and restore as pure state transitions rather than DOM bookkeeping.
`react-rnd` handles the pointer mechanics; everything above it — focus stacking,
minimise and restore, per-window persistence — is application state.

TODO: what made the z-order and focus model harder than it looked.

## Theme derived from the wallpaper

Colours are generated at runtime from the active wallpaper using Material's
colour utilities, so the entire interface retints when the background changes
while staying within contrast bounds.

TODO: how the palette is constrained so text stays readable on every wallpaper.
