/**
 * Identity and off-site links, in one place.
 *
 * The about page, the footer and anything else that needs a handle read from
 * here, so changing one is a single edit rather than a grep. Everything is a
 * plain string baked into the HTML at build time -- nothing here is fetched at
 * runtime, which is part of why the CSP can keep `connect-src 'self'`.
 */

export const person = {
  name: 'Pawan Tamada',
  /* Short form, used for the wordmark and the `Title — Pawan` suffix. */
  short: 'Pawan',
  role: 'Software Engineer',
  location: 'Mumbai, India',
  email: 'pawantamada8@gmail.com',
  /* Vendored into public/ rather than linked off the old site's CDN: the CSP
     is `default-src 'none'`, and a portfolio that 404s because someone else's
     bucket moved is worse than a 117KB file in the binary. */
  resume: '/pawan-tamada-resume.pdf',
} as const;

interface SocialLink {
  label: string;
  href: string;
  /** What to render as the link text. Falls back to the label. */
  handle?: string;
  /** Shown in the footer's compact row. Work-relevant links only. */
  primary?: boolean;
}

/* Ordered by how likely a visitor is to want them, not alphabetically. */
export const socials: SocialLink[] = [
  { label: 'GitHub', href: 'https://github.com/pawan67', handle: 'pawan67', primary: true },
  {
    label: 'LinkedIn',
    href: 'https://www.linkedin.com/in/pawan67/',
    handle: 'in/pawan67',
    primary: true,
  },
  { label: 'Email', href: `mailto:${person.email}`, handle: person.email, primary: true },
  { label: 'Instagram', href: 'https://www.instagram.com/pawan.dwr/', handle: 'pawan.dwr' },
  {
    label: 'Spotify',
    href: 'https://open.spotify.com/user/rvh3hauw81725txr97vo0jnaw?si=ca46335c0d164907',
    handle: 'What I listen to',
  },
];

export const primarySocials = socials.filter((s) => s.primary);
