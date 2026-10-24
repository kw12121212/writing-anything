/**
 * Stage the built-in wa-* skills into the plugin's skills/ directory.
 *
 * The plugin bundles all 13 built-in skills verbatim from the repository's
 * single source of truth (internal/skills/skills/). The roster below is the
 * contract: a missing or extra skill directory fails the build, and every
 * staged SKILL.md must be byte-identical to its source. The plugin's own
 * writing-workspace skill is source-owned in this package and never touched.
 */
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
const sourceDir = fileURLToPath(new URL('../../../internal/skills/skills', import.meta.url));
const targetDir = `${root}/skills`;

const ROSTER = [
  'wa-assemble',
  'wa-auto',
  'wa-brief',
  'wa-continuity',
  'wa-draft',
  'wa-illustrate',
  'wa-init',
  'wa-outline',
  'wa-plan',
  'wa-publish',
  'wa-review',
  'wa-revise',
  'wa-scene',
];

const staged = new Set(readdirSync(targetDir).filter((name) => name !== 'writing-workspace'));
for (const name of staged) {
  if (!ROSTER.includes(name)) {
    console.error(`[stage-skills] unexpected skill directory skills/${name}; update the ROSTER when the built-in roster changes`);
    process.exit(1);
  }
}

for (const name of ROSTER) {
  const from = `${sourceDir}/${name}`;
  const to = `${targetDir}/${name}`;
  if (!existsSync(from) || !statSync(from).isDirectory()) {
    console.error(`[stage-skills] missing built-in skill ${name} under ${sourceDir}`);
    process.exit(1);
  }
  rmSync(to, { recursive: true, force: true });
  cpSync(from, to, { recursive: true });
  const source = readFileSync(`${from}/SKILL.md`, 'utf8');
  const copy = readFileSync(`${to}/SKILL.md`, 'utf8');
  if (source !== copy) {
    console.error(`[stage-skills] staged copy of ${name}/SKILL.md differs from the source`);
    process.exit(1);
  }
  console.log(`[stage-skills] ${name} staged (${copy.length} bytes)`);
}
mkdirSync(targetDir, { recursive: true });
console.log(`[stage-skills] ${ROSTER.length} built-in skills staged under skills/`);
