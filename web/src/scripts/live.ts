// Live origin panel. Subscribes to the server's metrics stream.
//
// EventSource reconnects on its own, so there is no retry logic here; the only
// state worth tracking is whether the connection is currently up.

type Snapshot = {
  at: string;
  cpuPercent: number;
  memUsed: number;
  memTotal: number;
  memPercent: number;
  load1: number;
  load5: number;
  load15: number;
  uptime: number;
  diskUsed?: number;
  diskTotal?: number;
  diskPercent?: number;
  goroutines: number;
  heapBytes: number;
  procUptime: number;
  requestsTotal: number;
  requestsPerSec: number;
};

const gib = 1024 ** 3;
const mib = 1024 ** 2;

function bytes(n: number): string {
  return n >= gib ? `${(n / gib).toFixed(1)}GB` : `${Math.round(n / mib)}MB`;
}

function duration(seconds: number): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);

  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

function set(name: string, value: string, detail?: string) {
  const cell = document.querySelector<HTMLElement>(`[data-live="${name}"]`);
  if (!cell) return;

  const valueEl = cell.querySelector<HTMLElement>('[data-live-value]');
  if (valueEl) valueEl.textContent = value;

  const detailEl = cell.querySelector<HTMLElement>('[data-live-detail]');
  if (detailEl && detail !== undefined) detailEl.textContent = detail;
}

function render(s: Snapshot) {
  set('cpu', `${s.cpuPercent.toFixed(1)}%`, `load ${s.load1.toFixed(2)} · ${s.load5.toFixed(2)} · ${s.load15.toFixed(2)}`);
  set('memory', `${s.memPercent.toFixed(1)}%`, `${bytes(s.memUsed)} of ${bytes(s.memTotal)}`);
  set('requests', `${s.requestsPerSec.toFixed(1)}/s`, `${s.requestsTotal.toLocaleString()} since boot`);
  set('uptime', duration(s.uptime), `process up ${duration(s.procUptime)}`);
  set('process', `${bytes(s.heapBytes)}`, `${s.goroutines} goroutines`);

  if (s.diskTotal) {
    set('disk', `${(s.diskPercent ?? 0).toFixed(1)}%`, `${bytes(s.diskUsed ?? 0)} of ${bytes(s.diskTotal)}`);
  }
}

function setStatus(state: 'live' | 'reconnecting' | 'unsupported') {
  const el = document.querySelector<HTMLElement>('[data-live-status]');
  if (!el) return;

  el.dataset.state = state;
  el.textContent =
    state === 'live' ? 'live' : state === 'reconnecting' ? 'reconnecting' : 'unavailable';
}

function start() {
  const root = document.querySelector<HTMLElement>('[data-live-panel]');
  if (!root) return;

  if (typeof EventSource === 'undefined') {
    setStatus('unsupported');
    return;
  }

  const source = new EventSource('/api/metrics/stream');

  source.addEventListener('message', (event) => {
    try {
      render(JSON.parse(event.data) as Snapshot);
      root.dataset.state = 'loaded';
      setStatus('live');
    } catch {
      // A malformed frame is not worth tearing the stream down for.
    }
  });

  source.addEventListener('error', () => setStatus('reconnecting'));

  // Do not hold a connection open for a tab nobody is looking at.
  addEventListener('pagehide', () => source.close());
}

start();
