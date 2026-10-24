/**
 * Assemble the runtime artifacts the plugin manifest promises:
 *
 * - dist/server.js  — tsdown ESM output for src/server.ts, used as-is.
 * - dist/panel.html — src/panel-template.html with dist/panel.js inlined as
 *   the only script, served by the MCP server as its ui:// panel resource.
 *
 * The panel page must be self-contained (the ui:// resource is the only thing
 * the host loads; there is no sibling asset URL to rely on), so the bundled
 * script is inlined verbatim into the template's <script> slot. Fails loudly
 * when an expected artifact is missing so a silent manifest break cannot ship.
 */
import { existsSync, readFileSync, readdirSync, renameSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const dist = `${root}/dist`;

const names = readdirSync(dist);
const serverEntry = `${dist}/server.js`;
const panelMatch = names.find((name) => /^panel(\..+)?\.js$/.test(name));
if (panelMatch === undefined) {
  console.error(`[normalize-outputs] missing panel bundle under ${dist}: found ${JSON.stringify(names)}`);
  process.exit(1);
}
renameSync(`${dist}/${panelMatch}`, `${dist}/panel.js`);
console.log(`[normalize-outputs] ${panelMatch} -> panel.js`);
const panelBundle = `${dist}/panel.js`;
const template = readFileSync(`${root}/src/panel-template.html`, 'utf8');

for (const [path, label] of [
  [serverEntry, 'server entry (tsdown output for src/server.ts)'],
  [panelBundle, 'panel bundle (tsdown output for src/panel.ts)'],
]) {
  if (!existsSync(path)) {
    console.error(`[normalize-outputs] missing ${label}: ${path}`);
    process.exit(1);
  }
}

if (!template.includes('<!-- PANEL_SCRIPT -->')) {
  console.error('[normalize-outputs] src/panel-template.html has no <!-- PANEL_SCRIPT --> slot');
  process.exit(1);
}

const script = readFileSync(panelBundle, 'utf8');
const html = template.replace('<!-- PANEL_SCRIPT -->', () => `<script>\n${script}\n</script>`);
writeFileSync(`${dist}/panel.html`, html);
console.log(`[normalize-outputs] dist/panel.html written (${html.length} bytes)`);
