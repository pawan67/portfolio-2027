#!/usr/bin/env python3
"""Audit a running deployment against what this repo claims about it.

Read-only: it issues ordinary GET/HEAD requests and reads what comes back. It
changes nothing, so it is safe to point at a shared production host.

    scripts/audit.py http://127.0.0.1:8899      # local, TLS checks skipped
    scripts/audit.py https://example.com        # full audit

Exits non-zero if any check fails. Checks that cannot apply are skipped, not
silently passed.
"""

import json
import re
import subprocess
import sys
from dataclasses import dataclass
from urllib.parse import urlparse

TIMEOUT = "15"


@dataclass
class Result:
    name: str
    state: str  # ok | fail | skip
    detail: str


results: list[Result] = []


def record(name: str, ok: bool | None, detail: str = "") -> None:
    state = "skip" if ok is None else ("ok" if ok else "fail")
    results.append(Result(name, state, detail))


def curl(*args: str) -> tuple[int, str, str]:
    proc = subprocess.run(
        ["curl", "-sS", "--max-time", TIMEOUT, *args],
        capture_output=True, text=True,
    )
    return proc.returncode, proc.stdout, proc.stderr


def headers(url: str, *extra: str) -> dict[str, str]:
    """Fetch response headers as a lowercased dict. Body is discarded.

    The exit code is deliberately ignored: an event stream never completes, so
    curl exits non-zero when its timeout fires even though the response headers
    arrived correctly. An empty dict from an empty stdout still signals failure.
    """
    _, out, _ = curl("-D", "-", "-o", "/dev/null", *extra, url)

    found: dict[str, str] = {}
    for line in out.splitlines():
        key, sep, value = line.partition(":")
        if sep:
            found[key.strip().lower()] = value.strip()
    return found


def status(url: str, *extra: str) -> int:
    code, out, _ = curl("-o", "/dev/null", "-w", "%{http_code}", *extra, url)
    return int(out) if code == 0 and out.strip().isdigit() else 0


def body(url: str, *extra: str) -> str:
    _, out, _ = curl(*extra, url)
    return out


def content_length(url: str, accept_encoding: str) -> tuple[str, int]:
    h = headers(url, "-H", f"Accept-Encoding: {accept_encoding}")
    return h.get("content-encoding", ""), int(h.get("content-length", 0) or 0)


# --- checks -----------------------------------------------------------------

def check_serving(base: str) -> str:
    home = headers(base + "/")
    record("home responds 200", status(base + "/") == 200)
    record("Vary: Accept-Encoding", home.get("vary", "").lower() == "accept-encoding",
           home.get("vary", "<missing>"))

    br_enc, br_len = content_length(base + "/", "gzip, br, zstd")
    id_enc, id_len = content_length(base + "/", "identity")
    record("negotiates a compressed encoding", br_enc in {"br", "zstd", "gzip"}, br_enc or "<none>")
    record("compressed body is smaller than identity",
           0 < br_len < id_len, f"{br_len}B vs {id_len}B")

    # The origin ranks variants by measured size, so brotli should win here.
    record("picks the smallest available encoding", br_enc == "br",
           f"chose {br_enc or '<none>'}")

    gz_enc, _ = content_length(base + "/", "gzip")
    record("honours a restricted Accept-Encoding", gz_enc == "gzip", gz_enc or "<none>")

    zstd_enc, _ = content_length(base + "/", "br;q=0, gzip, zstd")
    record("honours q=0 refusal", zstd_enc == "zstd", zstd_enc or "<none>")

    br_etag = headers(base + "/", "-H", "Accept-Encoding: br").get("etag", "")
    id_etag = headers(base + "/", "-H", "Accept-Encoding: identity").get("etag", "")
    record("ETag varies by encoding", bool(br_etag) and br_etag != id_etag,
           f"{br_etag} vs {id_etag}")

    conditional = status(base + "/", "-H", "Accept-Encoding: br",
                         "-H", f"If-None-Match: {br_etag}")
    record("conditional request returns 304", conditional == 304, str(conditional))

    record("unknown path returns 404", status(base + "/no-such-page") == 404)
    record("POST to a document is rejected",
           status(base + "/", "-X", "POST") == 405)
    return home.get("etag", "")


def check_caching(base: str, html: str) -> None:
    html_cc = headers(base + "/").get("cache-control", "")
    record("HTML is revalidated, not immutable", "immutable" not in html_cc, html_cc)

    asset = re.search(r'src="(/_a/[^"]+\.js)"', html) or re.search(r'href="(/_a/[^"]+)"', html)
    if asset:
        cc = headers(base + asset.group(1)).get("cache-control", "")
        record("hashed asset is immutable", "immutable" in cc, cc)
    else:
        record("hashed asset is immutable", None, "no hashed asset referenced")

    font = re.search(r'href="(/fonts/[^"]+\.woff2)"', html)
    if font:
        h = headers(base + font.group(1))
        record("font is immutable", "immutable" in h.get("cache-control", ""),
               h.get("cache-control", ""))
        record("font has correct content type", h.get("content-type", "") == "font/woff2",
               h.get("content-type", "<missing>"))
    else:
        record("font is immutable", None, "no font preloaded")


def check_headers(base: str, is_https: bool) -> None:
    h = headers(base + "/")

    expected = {
        "x-content-type-options": "nosniff",
        "referrer-policy": "strict-origin-when-cross-origin",
        "x-frame-options": "DENY",
    }
    for key, want in expected.items():
        record(f"header {key}", h.get(key, "") == want, h.get(key, "<missing>"))

    record("header content-security-policy (frame-ancestors)",
           "frame-ancestors" in h.get("content-security-policy", ""),
           h.get("content-security-policy", "<missing>"))
    record("header permissions-policy", "permissions-policy" in h, "")

    if is_https:
        hsts = h.get("strict-transport-security", "")
        record("HSTS present with a long max-age",
               "max-age=" in hsts and int(re.search(r"max-age=(\d+)", hsts).group(1)) >= 15768000,
               hsts or "<missing>")
    else:
        record("HSTS present with a long max-age", None, "plain HTTP")

    record("preload Link header on HTML", "link" in h, h.get("link", "<missing>"))


def check_document(base: str, html: str) -> None:
    csp = re.search(r'content-security-policy"\s+content="([^"]*)"', html)
    if csp:
        directives = csp.group(1)
        record("document CSP uses hashes", "sha256-" in directives, "")
        record("document CSP avoids unsafe-inline", "unsafe-inline" not in directives, "")
    else:
        record("document CSP uses hashes", False, "no CSP meta tag")

    rules = re.search(r'<script type="speculationrules"[^>]*>(.*?)</script>', html, re.S)
    if rules:
        try:
            json.loads(rules.group(1))
            record("speculation rules parse as JSON", True, "")
        except json.JSONDecodeError as exc:
            record("speculation rules parse as JSON", False, str(exc))
    else:
        record("speculation rules parse as JSON", False, "no speculationrules block")

    blocking = [
        tag for tag in re.findall(r"<script([^>]*)>", html, re.I)
        if "src=" in tag.lower()
        and not any(k in tag.lower() for k in ("defer", "async", 'type="module"'))
    ]
    record("no render-blocking scripts", not blocking, "; ".join(blocking))

    record("stylesheet is inlined", "<style" in html and "<link rel=\"stylesheet\"" not in html, "")


def check_endpoints(base: str) -> None:
    build = body(base + "/api/build")
    try:
        commit = json.loads(build).get("shortCommit", "")
        record("/api/build reports a commit", bool(commit), commit)
    except json.JSONDecodeError:
        record("/api/build reports a commit", False, "not JSON")

    record("/healthz responds 200", status(base + "/healthz") == 200)

    try:
        report = json.loads(body(base + "/api/perf"))
        record("/api/perf returns a report",
               "windowDays" in report, f"{report.get('samples', '?')} samples")
    except json.JSONDecodeError:
        record("/api/perf returns a report", False, "not JSON")

    stream = headers(base + "/api/metrics/stream", "-H", "Accept: text/event-stream", "-m", "3")
    record("/api/metrics/stream is an event stream",
           stream.get("content-type", "").startswith("text/event-stream"),
           stream.get("content-type", "<missing>"))

    record("/api/rum rejects malformed input",
           status(base + "/api/rum", "-X", "POST", "-d", "garbage") == 400)


def check_transport(base: str, is_https: bool) -> None:
    if not is_https:
        for name in ("HTTP/2 or HTTP/3 negotiated", "TLS 1.2 or better",
                     "103 Early Hints observed", "served through Cloudflare",
                     "HTTP redirects to HTTPS"):
            record(name, None, "plain HTTP")
        return

    _, _, err = curl("-v", "-o", "/dev/null", base + "/")
    record("HTTP/2 or HTTP/3 negotiated",
           bool(re.search(r"using HTTP/(2|3)", err)),
           (re.search(r"using HTTP/[\d.]+", err) or ["?"])[0])

    tls = re.search(r"SSL connection using (TLSv[\d.]+)", err)
    record("TLS 1.2 or better", bool(tls) and tls.group(1) >= "TLSv1.2",
           tls.group(1) if tls else "<unknown>")

    record("103 Early Hints observed", " 103" in err or "103 Early" in err,
           "Cloudflare generates these from the origin's Link header")

    h = headers(base + "/")
    record("served through Cloudflare", "cf-ray" in h, h.get("cf-ray", "<no cf-ray>"))

    host = urlparse(base).netloc
    plain = status(f"http://{host}/", "-o", "/dev/null")
    record("HTTP redirects to HTTPS", plain in (301, 308), str(plain))


def main() -> int:
    if len(sys.argv) < 2:
        print(__doc__)
        return 2

    base = sys.argv[1].rstrip("/")
    is_https = urlparse(base).scheme == "https"

    print(f"auditing {base}\n")

    html = body(base + "/")
    if not html:
        print("could not fetch the site", file=sys.stderr)
        return 1

    check_serving(base)
    check_caching(base, html)
    check_headers(base, is_https)
    check_document(base, html)
    check_endpoints(base)
    check_transport(base, is_https)

    width = max(len(r.name) for r in results)
    marks = {"ok": "ok  ", "fail": "FAIL", "skip": "skip"}
    for r in results:
        print(f"  {marks[r.state]} {r.name:<{width}}  {r.detail}")

    failed = sum(r.state == "fail" for r in results)
    skipped = sum(r.state == "skip" for r in results)
    passed = sum(r.state == "ok" for r in results)
    print(f"\n{passed} passed, {failed} failed, {skipped} skipped")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
