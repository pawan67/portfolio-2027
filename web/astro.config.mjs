// @ts-check
import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import sitemap from '@astrojs/sitemap';
import tailwindcss from '@tailwindcss/vite';

// Set SITE_URL at build time. Sitemap + canonical URLs depend on it.
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

  integrations: [react(), sitemap()],

  vite: {
    plugins: [tailwindcss()],
    build: {
      cssCodeSplit: false,
      assetsInlineLimit: 4096,
    },
  },
});
