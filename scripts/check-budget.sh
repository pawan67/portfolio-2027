#!/usr/bin/env bash
# Enforce the performance budget against real build output.
# Run after `pnpm build` in web/. Exits non-zero if any budget is blown.
set -euo pipefail

DIST="${1:-web/dist}"

# 14KB is roughly the initial TCP congestion window: an HTML document under it
# arrives in a single round trip.
MAX_HTML_BR=14336
MAX_CSS=10240

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
echo "== CSS per page (inlined + external, budget ${MAX_CSS}B)"
external=$(find "$DIST" -name '*.css' -exec cat {} + 2>/dev/null | wc -c)
while IFS= read -r -d '' f; do
  inlined=$(awk '/<style/{i=1} i{n+=length($0)+1} /<\/style>/{i=0} END{print n+0}' "$f")
  total=$(( inlined + external ))
  rel=${f#"$DIST"/}
  if (( total > MAX_CSS )); then
    printf '  FAIL %-40s %6dB (inline %d, external %d)\n' "$rel" "$total" "$inlined" "$external"
    fail=1
  else
    printf '  ok   %-40s %6dB (inline %d, external %d)\n' "$rel" "$total" "$inlined" "$external"
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
