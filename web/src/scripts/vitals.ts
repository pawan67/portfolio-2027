// Field measurement beacon. Runs on every page.
//
// This is the only JavaScript the site ships by default. It is a module script,
// so it is deferred and never blocks rendering, and it posts to the origin's own
// ingest endpoint -- no third-party analytics, no cookies, no identifiers.
import { onCLS, onFCP, onINP, onLCP, onTTFB, type Metric } from 'web-vitals';

type Sample = { name: string; value: number };

const pending = new Map<string, Sample>();
let flushTimer: ReturnType<typeof setTimeout> | undefined;

function flush() {
  clearTimeout(flushTimer);
  flushTimer = undefined;

  if (pending.size === 0) return;

  // Links are prerendered on hover. A prerender that is discarded without ever
  // being activated was never seen by anyone, and reporting it would pollute
  // the field data with page loads that did not happen.
  if (document.prerendering) return;

  const body = JSON.stringify({ metrics: [...pending.values()] });
  pending.clear();

  // sendBeacon survives the page being discarded, which is exactly when the
  // final CLS and INP values become available.
  navigator.sendBeacon?.('/api/rum', new Blob([body], { type: 'application/json' }));
}

function record(metric: Metric) {
  pending.set(metric.name, { name: metric.name, value: metric.value });

  // Coalesce the metrics that land early (TTFB, FCP, LCP) into one request
  // rather than sending three. CLS and INP arrive at teardown and are caught by
  // the visibilitychange handler below.
  if (flushTimer === undefined) {
    flushTimer = setTimeout(flush, 1000);
  }
}

onLCP(record);
onINP(record);
onCLS(record);
onTTFB(record);
onFCP(record);

addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'hidden') flush();
});

// Safari does not reliably fire visibilitychange on navigation away.
addEventListener('pagehide', flush);
