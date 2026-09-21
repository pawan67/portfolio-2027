---
title: Selfhost Infra
summary: The Compose stack everything else here runs on — Traefik in front, Authelia at the door, every service a profile you can switch off.
year: 2025
stack: ["Docker Compose", "Traefik", "Authelia", "Cloudflare DDNS"]
role: "Engineering"
repo: "https://github.com/pawan67/selfhost-infra-docker"
order: 6
---

The substrate under the rest of this list. One VPS, Traefik terminating and
routing everything, Authelia in front of anything that should not be open, and
Let's Encrypt certificates issued without a step anyone has to remember to
repeat.

What makes it usable as a personal stack rather than a one-off is that services
are Compose profiles. Nothing is load-bearing except Traefik and Authelia, which
come up under a `required` profile; everything else is a name in an environment
variable, so turning a service on or off is an edit and a `docker compose up -d`
rather than a surgical operation on a YAML file. Each app keeps its own
`.env.example` next to it instead of a single sprawling file at the root.

DNS is handled by a Cloudflare DDNS container, so records follow the host
without manual updates.

It is deliberately a repository rather than a directory on a server. The whole
arrangement is declared in git, which is the only reason rebuilding it is a
clone and a handful of secrets instead of an archaeology exercise.
