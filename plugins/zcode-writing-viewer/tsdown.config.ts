import { defineConfig } from 'tsdown';

export default defineConfig([
  {
    // Server half: the MCP stdio server the ZCode manifest launches with
    // node dist/server.mjs (renamed from server.js by normalize-outputs).
    entry: ['src/server.ts'],
    outDir: 'dist',
    format: 'esm',
    dts: true,
    platform: 'node',
    outExtensions: () => ({ js: '.js' }),
    // Self-contained: the installed plugin ships dist/ only, no node_modules,
    // so every dependency (SDK, zod, ajv, ...) is bundled like the official
    // ZCode UI plugins do.
  },
  {
    // Panel half: a self-contained HTML document the server serves through
    // its ui:// panel resource. The bundler inlines the script into the
    // template (see scripts/inline-panel.mjs), so the emitted bundle must be
    // a plain script: no imports, no exports, no globals leaked beyond the
    // one app bootstrap. yaml and the official ext-apps client are bundled
    // intentionally — the page loads no external code.
    entry: ['src/panel.ts'],
    outDir: 'dist',
    format: 'iife',
    dts: false,
    platform: 'browser',
    outExtensions: () => ({ js: '.js' }),
    outputOptions: { name: 'zcodeWritingViewerPanel' },
  },
]);
