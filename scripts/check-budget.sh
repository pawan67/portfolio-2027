#!/usr/bin/env bash
# Enforce the performance budget against real build output.
# Run after `pnpm build` in web/. Exits non-zero if any budget is blown.
set -euo pipefail

DIST="${1:-web/dist}"

# 14KB is roughly the initial TCP congestion window: an HTML document under it
# arrives in a single round trip.
MAX_HTML_BR=14336

# Two CSS budgets, because they guard different costs.
#
# The compressed number is what users pay to download and is the tighter guard.
# The uncompressed number guards parse cost and unbounded growth.
#
# Revised from an initial 10KB uncompressed, which was set before any CSS
# existed and turned out to be below Tailwind's fixed floor: preflight alone is
# ~4.3KB and the theme layer ~1.2KB before a single utility is emitted.
MAX_CSS_BR=6144
MAX_CSS=16384

fail=0

echo "== HTML (brotli, budget ${MAX_HTML_BR}B)"
while IFS= read -r -d '' f; do
  size=$(brotli -q 11 -c "$f" | wc -c)
  rel=${f#"$DIST"/}
  if (( size > MAX_HTML_BR )); then
    printf '  FAIL %-40s %6dB\n' "$rel" "$size"
    fail=1
  else
    printf '  ok   %-40s %6dB\n' "$rel" "$size"
  fi
done < <(find "$DIST" -name '*.html' -print0)

# CSS is inlined into every document, so the meaningful budget is per page,
# not the sum across the site.
echo "== CSS per page (budget ${MAX_CSS_BR}B brotli / ${MAX_CSS}B raw)"
external=$(find "$DIST" -name '*.css' -exec cat {} + 2>/dev/null | wc -c)
while IFS= read -r -d '' f; do
  raw=$(awk '/<style/{i=1} i{n+=length($0)+1} /<\/style>/{i=0} END{print n+0}' "$f")
  raw=$(( raw + external ))
  br=$(awk '/<style/{i=1} i{print} /<\/style>/{i=0}' "$f" | brotli -q 11 -c | wc -c)
  rel=${f#"$DIST"/}

  if (( br > MAX_CSS_BR || raw > MAX_CSS )); then
    printf '  FAIL %-36s %5dB br / %6dB raw\n' "$rel" "$br" "$raw"
    fail=1
  else
    printf '  ok   %-36s %5dB br / %6dB raw\n' "$rel" "$br" "$raw"
  fi
done < <(find "$DIST" -name '*.html' -print0)

# Astro inlines CSS, so any <script src> on the landing page is unintended JS.
echo "== JS on the landing page (budget: none)"
if grep -qE '<script[^>]+src=' "$DIST/index.html"; then
  echo "  FAIL index.html loads an external script"
  grep -oE '<script[^>]+src="[^"]+"' "$DIST/index.html" | sed 's/^/    /'
  fail=1
else
  echo "  ok   no external scripts"
fi

exit $fail
