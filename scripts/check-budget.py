#!/usr/bin/env python3
"""Enforce the performance budget against real build output.

Run after `pnpm build` in web/. Exits non-zero if any budget is blown.
"""

import re
import subprocess
import sys
from pathlib import Path

# 14KB is roughly the initial TCP congestion window: a document under it
# arrives in a single round trip.
MAX_HTML_BR = 14_336

# Two CSS budgets, guarding different costs. The compressed number is what
# visitors pay to download; the raw number guards parse cost and unbounded
# growth. Revised up from an initial 10KB raw, which was set before any CSS
# existed and sat below Tailwind's floor (preflight alone is ~4.3KB).
MAX_CSS_BR = 6_144
MAX_CSS_RAW = 16_384

# Field measurement cannot be done without shipping code. The rule is that none
# of it blocks rendering, and that the amount stays bounded.
MAX_DEFERRED_JS_BR = 4_096

STYLE_RE = re.compile(r"<style[^>]*>(.*?)</style>", re.S)
SCRIPT_RE = re.compile(r"<script([^>]*)>", re.I)
SRC_RE = re.compile(r"""src=["']([^"']+)["']""", re.I)


def brotli(data: bytes) -> int:
    return len(subprocess.run(["brotli", "-q", "11", "-c"], input=data,
                              capture_output=True, check=True).stdout)


def render_blocking(attrs: str) -> bool:
    """A script with src blocks rendering unless deferred, async, or a module."""
    if not SRC_RE.search(attrs):
        return False  # inline scripts are counted against the HTML budget
    lowered = attrs.lower()
    return not ("defer" in lowered or "async" in lowered or 'type="module"' in lowered)


def main() -> int:
    dist = Path(sys.argv[1] if len(sys.argv) > 1 else "web/dist")
    pages = sorted(dist.rglob("*.html"))
    if not pages:
        print(f"no HTML found under {dist}", file=sys.stderr)
        return 1

    failed = False
    external_css = sum(f.stat().st_size for f in dist.rglob("*.css"))

    print(f"== HTML, brotli (budget {MAX_HTML_BR}B)")
    for page in pages:
        size = brotli(page.read_bytes())
        ok = size <= MAX_HTML_BR
        failed |= not ok
        print(f"  {'ok  ' if ok else 'FAIL'} {page.relative_to(dist)!s:<36} {size:6d}B")

    print(f"== CSS per page (budget {MAX_CSS_BR}B brotli / {MAX_CSS_RAW}B raw)")
    for page in pages:
        inlined = "".join(STYLE_RE.findall(page.read_text()))
        raw = len(inlined) + external_css
        br = brotli(inlined.encode()) if inlined else 0
        ok = br <= MAX_CSS_BR and raw <= MAX_CSS_RAW
        failed |= not ok
        print(f"  {'ok  ' if ok else 'FAIL'} {page.relative_to(dist)!s:<36} "
              f"{br:5d}B br / {raw:6d}B raw")

    print("== Render-blocking JS (budget: none)")
    for page in pages:
        blocking = [a for a in SCRIPT_RE.findall(page.read_text()) if render_blocking(a)]
        failed |= bool(blocking)
        print(f"  {'ok  ' if not blocking else 'FAIL'} {page.relative_to(dist)!s:<36} "
              f"{len(blocking)} blocking")
        for attrs in blocking:
            print(f"       <script{attrs}>")

    # Inline scripts already count against the HTML budget, so only external
    # files are measured here -- counting both would double-charge them.
    print(f"== Deferred external JS per page (budget {MAX_DEFERRED_JS_BR}B brotli)")
    for page in pages:
        total = 0
        for attrs in SCRIPT_RE.findall(page.read_text()):
            m = SRC_RE.search(attrs)
            if not m:
                continue
            asset = dist / m.group(1).lstrip("/")
            if asset.is_file():
                total += brotli(asset.read_bytes())
        ok = total <= MAX_DEFERRED_JS_BR
        failed |= not ok
        print(f"  {'ok  ' if ok else 'FAIL'} {page.relative_to(dist)!s:<36} {total:5d}B br")

    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
