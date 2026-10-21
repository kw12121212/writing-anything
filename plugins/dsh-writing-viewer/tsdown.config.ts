import { defineConfig } from 'tsdown';

export default defineConfig([
  {
    // Host half: an ESM cordis plugin loaded by the profile's plugin loader
    // through the package root export.
    entry: ['src/host.ts'],
    outDir: 'dist',
    format: 'esm',
    dts: true,
    platform: 'node',
    outExtensions: () => ({ js: '.js' }),
  },
  {
    // Browser half: a self-contained lazy-CJS definition handed to
    // window.__ModuleLoader__.load. No imports, no exports — the file's only
    // top-level action is the load call, so the emitted bundle stays a plain
    // script whatever format the host serves it as. The iife name is fixed
    // to keep the bundler from warning about anonymous exports, and yaml is
    // declared as intentionally bundled (the parser ships inside the client).
    entry: ['src/client.ts'],
    outDir: 'dist',
    format: 'iife',
    dts: false,
    platform: 'browser',
    outExtensions: () => ({ js: '.js' }),
    outputOptions: { name: 'dshWritingViewerClient' },
    deps: { onlyBundle: ['yaml'] },
  },
]);
