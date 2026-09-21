// Footer now-playing line. One fetch, no polling.
//
// The origin caches its answer for the length of its own poll interval, so this
// is a cheap conditional-ish request that Cloudflare usually serves. There is
// no refresh timer: a footer that rewrites itself while someone is reading the
// page above it is a distraction, not a feature.

type Now = {
  playing: boolean;
  title: string;
  artist: string;
  url?: string;
  preview?: string;
};

const row = document.querySelector<HTMLElement>('[data-now-playing]');
if (row) fill(row);

async function fill(row: HTMLElement) {
  let now: Now;
  try {
    const res = await fetch('/api/listening');
    // 204 means the origin has nothing to publish -- no credentials, or no
    // successful poll yet. Either way there is no row to show.
    if (res.status !== 200) return row.remove();
    now = await res.json();
  } catch {
    return row.remove();
  }

  const label = row.querySelector('[data-np-label]')!;
  const link = row.querySelector<HTMLAnchorElement>('[data-np-link]')!;

  label.textContent = now.playing ? 'Now playing' : 'Last played';
  link.textContent = `${now.title} — ${now.artist}`;
  if (now.url) link.href = now.url;
  row.hidden = false;

  if (!now.preview) return;

  const audio = row.querySelector<HTMLAudioElement>('[data-np-audio]')!;
  const play = row.querySelector<HTMLButtonElement>('[data-np-play]')!;
  const idle = `Play a 30-second sample of ${now.title}`;

  audio.src = now.preview;
  play.ariaLabel = idle;
  play.hidden = false;

  play.addEventListener('click', () => {
    if (audio.paused) audio.play().catch(() => play.remove());
    else audio.pause();
  });

  const sync = () => {
    play.toggleAttribute('data-playing', !audio.paused);
    play.ariaLabel = audio.paused ? idle : 'Stop the sample';
  };
  audio.addEventListener('play', sync);
  audio.addEventListener('pause', sync);

  // Rewinding fires timeupdate, which drains the progress rail back to zero.
  audio.addEventListener('ended', () => {
    audio.currentTime = 0;
  });

  audio.addEventListener('timeupdate', () => {
    const done = audio.duration ? audio.currentTime / audio.duration : 0;
    row.style.setProperty('--np-progress', `${done}`);
  });
}
