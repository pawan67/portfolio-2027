// @ts-check
import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';
import tailwindcss from '@tailwindcss/vite';

// Set SITE_URL at build time. Sitemap + canonical URLs depend on it.
//
// An empty string is a misconfiguration rather than "unset", and has to be
// caught separately: CI passes `SITE_URL=${{ vars.SITE_URL }}` as a build arg,
// so an undefined Actions variable arrives here as '' and overrides the
// Dockerfile's ARG default. `??` would let that through and Astro would fail
// with a bare "Invalid URL" pointing at nothing in particular. Falling back
// silently would be worse -- the build would succeed and bake example.com into
// every canonical tag and sitemap entry.
if (process.env.SITE_URL === '') {
  throw new Error(
    'SITE_URL is set but empty. In CI, set the SITE_URL repository variable ' +
      '(Settings -> Secrets and variables -> Actions -> Variables); it is baked ' +
      'into canonical URLs and the sitemap at build time.',
  );
}
const site = process.env.SITE_URL ?? 'https://example.com';

export default defineConfig({
  site,
  output: 'static',
  compressHTML: true,

  build: {
    // Budget is <=10KB CSS. Inlining it removes a render-blocking request
    // entirely, which beats cross-page caching at this size.
    inlineStylesheets: 'always',
    assets: '_a',
  },

  // No `prefetch` on purpose: Astro's prefetch ships a runtime script.
  // BaseLayout emits a declarative <script type="speculationrules"> block
  // instead, which the browser parses without executing any JS.

  // Hash-based CSP. Astro emits a <meta http-equiv> with sha256 hashes for the
  // inlined stylesheet, so we get strict CSP without 'unsafe-inline'.
  // frame-ancestors is not valid in a meta CSP -- the Go server sets that header.
  security: {
    csp: {
      algorithm: 'SHA-256',
      directives: [
        "default-src 'none'",
        "img-src 'self' data:",
        "font-src 'self'",
        "connect-src 'self'",
        "base-uri 'none'",
        "form-action 'none'",
        "manifest-src 'self'",
      ],
    },
  },

  markdown: {
    // Shiki styles tokens inline, which a hash-based CSP rejects. Prism emits
    // classes instead, so the theme lives in our own stylesheet.
    syntaxHighlight: 'prism',
  },

  // No React integration: nothing on the site is interactive enough to need it,
  // and including it emitted a 51KB (brotli) client runtime chunk that no page
  // referenced but every image still carried. Add it back when an island
  // genuinely earns it.
  integrations: [sitemap()],

  vite: {
    plugins: [tailwindcss()],
    build: {
      cssCodeSplit: false,
      assetsInlineLimit: 4096,
    },
  },
});
