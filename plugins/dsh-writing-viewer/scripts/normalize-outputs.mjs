/**
 * Normalize tsdown output names to the paths package.json's exports map
 * promises: dist/index.js (host ESM) and dist/client.js (browser iife).
 *
 * tsdown 0.14 appends the format suffix to non-ESM output (client.iife.js)
 * and ignores outExtensions next to its `define` input-option warning, so the
 * deterministic fix is the same normalize step dshmarket uses for its client
 * banner: rename after the build. Fails loudly when an expected artifact is
 * missing so a silent exports-map break cannot ship.
 */
import { readdirSync, renameSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const dist = fileURLToPath(new URL('../dist', import.meta.url));

const names = readdirSync(dist);

function renameOne(matches, target, label) {
  if (matches.length === 0) {
    console.error(`[normalize-outputs] missing ${label} artifact under ${dist}: found ${JSON.stringify(names)}`);
    process.exit(1);
  }
  renameSync(`${dist}/${matches[0]}`, `${dist}/${target}`);
  console.log(`[normalize-outputs] ${matches[0]} -> ${target}`);
}

renameOne(names.filter((name) => /^host.*\.js$/.test(name)), 'index.js', 'host entry');
renameOne(names.filter((name) => /^client(\..+)?\.js$/.test(name)), 'client.js', 'client entry');
