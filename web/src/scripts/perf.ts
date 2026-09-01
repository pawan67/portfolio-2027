// Renders the live sections of /perf. Loaded only on that page.

type MetricSummary = {
  metric: string;
  p50: number;
  p75: number;
  p95: number;
  samples: number;
  rating: 'good' | 'needs-improvement' | 'poor' | string;
  goodShare: number;
  unit: string;
};

type Report = {
  windowDays: number;
  generatedAt: string;
  samples: number;
  metrics: MetricSummary[];
  countries: { country: string; samples: number; lcpP75: number; rating: string }[];
};

const fmt = (value: number, unit: string) =>
  unit === 'ms' ? `${Math.round(value)}ms` : value.toFixed(3);

const pct = (share: number) => `${Math.round(share * 100)}%`;

function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  className?: string,
  text?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function ratingDot(rating: string) {
  const dot = el('span', 'perf-dot');
  dot.dataset.rating = rating;
  dot.setAttribute('role', 'img');
  dot.setAttribute('aria-label', rating.replace('-', ' '));
  return dot;
}

function renderMetrics(report: Report, into: HTMLElement) {
  into.replaceChildren();

  for (const m of report.metrics) {
    const row = el('div', 'perf-row');

    const head = el('div', 'perf-row-head');
    head.append(ratingDot(m.rating), el('span', 'perf-name', m.metric));
    head.append(el('span', 'perf-value', fmt(m.p75, m.unit)));

    const detail = el(
      'p',
      'perf-detail',
      `p50 ${fmt(m.p50, m.unit)} · p95 ${fmt(m.p95, m.unit)} · ` +
        `${pct(m.goodShare)} of samples rated good · ${m.samples.toLocaleString()} samples`,
    );

    row.append(head, detail);
    into.append(row);
  }
}

function renderCountries(report: Report, into: HTMLElement) {
  into.replaceChildren();

  // A single country's numbers say more about that visitor's network than about
  // the site, so the breakdown only earns its place once there is a comparison.
  if (report.countries.length < 2) {
    into.append(
      el('p', 'perf-note', 'Not enough regions yet to make a comparison meaningful.'),
    );
    return;
  }

  for (const c of report.countries.slice(0, 8)) {
    const row = el('div', 'perf-row-compact');
    // "??" is the ingest fallback when Cloudflare's country header is absent,
    // which happens on direct-to-origin hits.
    const label = c.country === '??' ? 'Unknown' : c.country;
    row.append(ratingDot(c.rating), el('span', 'perf-name', label));
    row.append(el('span', 'perf-value-sm', `${Math.round(c.lcpP75)}ms`));
    row.append(el('span', 'perf-detail', `${c.samples.toLocaleString()} samples`));
    into.append(row);
  }
}

async function loadReport() {
  const root = document.querySelector<HTMLElement>('[data-field-data]');
  const metricsInto = document.querySelector<HTMLElement>('[data-metrics]');
  const countriesInto = document.querySelector<HTMLElement>('[data-countries]');
  const summary = document.querySelector<HTMLElement>('[data-field-summary]');
  if (!root || !metricsInto || !countriesInto || !summary) return;

  let report: Report;
  try {
    const res = await fetch('/api/perf');
    if (!res.ok) throw new Error(`status ${res.status}`);
    report = await res.json();
  } catch {
    summary.textContent = 'Field data is temporarily unavailable.';
    return;
  }

  if (report.samples === 0) {
    summary.textContent =
      'No field data collected yet. This section fills in as people visit.';
    return;
  }

  summary.textContent =
    `Rolling ${report.windowDays} days · ${report.samples.toLocaleString()} samples · ` +
    `p75, the same statistic Chrome assesses against.`;

  renderMetrics(report, metricsInto);
  renderCountries(report, countriesInto);
  root.dataset.state = 'loaded';
}

// --- Edge versus origin -----------------------------------------------------

const ORIGIN_URL = import.meta.env.PUBLIC_ORIGIN_URL as string | undefined;

async function timeEndpoint(url: string, runs: number): Promise<number | null> {
  const samples: number[] = [];

  for (let i = 0; i < runs; i++) {
    const start = performance.now();
    try {
      // cache: 'no-store' keeps every run a real network round trip.
      await fetch(`${url}?t=${Date.now()}-${i}`, { cache: 'no-store', mode: 'cors' });
    } catch {
      return null;
    }
    samples.push(performance.now() - start);
  }

  samples.sort((a, b) => a - b);
  return samples[Math.floor(samples.length / 2)];
}

function setupComparison() {
  const button = document.querySelector<HTMLButtonElement>('[data-measure]');
  const output = document.querySelector<HTMLElement>('[data-measure-output]');
  if (!button || !output) return;

  button.addEventListener('click', async () => {
    button.disabled = true;
    button.textContent = 'Measuring…';
    output.textContent = '';

    const runs = 5;
    const edge = await timeEndpoint('/api/ping', runs);
    const origin = ORIGIN_URL ? await timeEndpoint(`${ORIGIN_URL}/api/ping`, runs) : null;

    output.replaceChildren();

    if (edge === null) {
      output.append(el('p', 'perf-note', 'Measurement failed.'));
    } else {
      const rows: [string, number | null][] = [
        ['Through Cloudflare', edge],
        ['Bare origin', origin],
      ];

      for (const [label, value] of rows) {
        if (value === null) continue;
        const row = el('div', 'perf-row-compact');
        row.append(el('span', 'perf-name', label));
        row.append(el('span', 'perf-value-sm', `${Math.round(value)}ms`));
        output.append(row);
      }

      output.append(
        el(
          'p',
          'perf-note',
          origin === null
            ? `Median of ${runs} round trips from your browser. The bare-origin comparison is not configured yet.`
            : `Median of ${runs} round trips from your browser, measured just now.`,
        ),
      );
    }

    button.disabled = false;
    button.textContent = 'Measure again';
  });
}

loadReport();
setupComparison();
