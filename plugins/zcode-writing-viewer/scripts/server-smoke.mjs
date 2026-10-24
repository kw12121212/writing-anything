/**
 * Server smoke: drive the built dist/server.js over stdio JSON-RPC exactly
 * like the ZCode host does — initialize, tools/list, tools/call
 * (workspace_status / workspace_snapshot against the testdata fixture) and
 * resources/read (the ui:// panel document and the illustration template
 * refusal without a session). The captured transcript lands in
 * docs/evidence/server-smoke.txt.
 */
import { spawn } from 'node:child_process';
import { cpSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, resolve as resolvePath } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolvePath(here, '..');
const server = resolvePath(root, 'dist', 'server.js');
// A temp copy of the shared fixture: on machines with a real wa-cli the
// snapshot tools append audit lines, which must not pollute the shared tree.
const fixtureSource = resolvePath(here, '..', '..', 'dsh-writing-viewer', 'testdata', 'workspace');
const fixture = mkdtempSync(resolvePath(tmpdir(), 'wv-smoke-'));
cpSync(fixtureSource, fixture, { recursive: true });

const child = spawn(process.execPath, [server], {
  cwd: fixture,
  stdio: ['pipe', 'pipe', 'pipe'],
});
child.stderr.on('data', (chunk) => process.stderr.write(`[server-stderr] ${chunk}`));

let nextId = 1;
const pending = new Map();
const log = [];
let stdoutBuffer = '';
child.stdout.on('data', (chunk) => {
  // One JSON-RPC line can span many data chunks (the 800 KiB panel document
  // does), so partial lines are buffered until the next newline arrives.
  stdoutBuffer += String(chunk);
  const lines = stdoutBuffer.split('\n');
  stdoutBuffer = lines.pop() ?? '';
  for (const line of lines) {
    if (line.trim() === '') continue;
    let message;
    try {
      message = JSON.parse(line);
    } catch {
      log.push(`[non-json stdout] ${line}`);
      continue;
    }
    log.push(`<- ${JSON.stringify(message)}`);
    const waiter = pending.get(message.id);
    if (waiter !== undefined) {
      pending.delete(message.id);
      waiter(message);
    }
  }
});

function request(method, params) {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    pending.set(id, (message) => {
      if (message.error !== undefined) reject(new Error(JSON.stringify(message.error)));
      else resolve(message.result);
    });
    const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params });
    log.push(`-> ${payload}`);
    child.stdin.write(payload + '\n');
  });
}

const fails = [];
function check(name, ok, detail = '') {
  log.push(`[check] ${name}: ${ok ? 'PASS' : 'FAIL'}${detail === '' ? '' : ` — ${detail}`}`);
  if (!ok) fails.push(name);
}

try {
  const init = await request('initialize', {
    protocolVersion: '2025-06-18',
    capabilities: {},
    clientInfo: { name: 'server-smoke', version: '0.0.0' },
  });
  check('initialize answers serverInfo', init?.serverInfo?.name === 'zcode-writing-viewer', JSON.stringify(init?.serverInfo ?? null));
  child.stdin.write(JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }) + '\n');

  const tools = await request('tools/list', {});
  const names = (tools.tools ?? []).map((tool) => tool.name);
  check(
    'tool roster',
    JSON.stringify(names) === JSON.stringify([
      'workspace_status', 'workspace_snapshot', 'open_workspace', 'show_in_viewer', 'get_view', 'report_view', 'release_view',
      'review_start', 'review_status', 'review_end', 'review_page', 'review_submit',
    ]),
    names.join(','),
  );
  check('panel-only visibility', tools.tools.find((t) => t.name === 'report_view')?._meta?.ui?.visibility?.[0] === 'app');
  check('setup tools absent by default', !names.some((name) => name.startsWith('setup_')));

  const status = await request('tools/call', { name: 'workspace_status', arguments: {} });
  const statusBody = status.structuredContent;
  check('workspace_status locates fixture', statusBody?.workspaceExists === true, JSON.stringify(statusBody?.root));

  const snapshot = await request('tools/call', { name: 'workspace_snapshot', arguments: {} });
  const works = snapshot.structuredContent?.snapshot?.works?.map((w) => w.id);
  check('workspace_snapshot aggregates works', JSON.stringify(works) === JSON.stringify(['my-novel', 'my-post']), String(works));

  const invalid = await request('tools/call', { name: 'workspace_snapshot', arguments: { root: 'relative/path' } });
  check('invalid panel root refused', typeof invalid.structuredContent?.rootError === 'string');

  const open = await request('tools/call', { name: 'open_workspace', arguments: { unit: 'my-post' } });
  check('open_workspace carries ui.surface meta', open._meta?.ui?.surface === 'writing-workspace');

  const resources = await request('resources/list', {});
  const uris = (resources.resources ?? []).map((r) => r.uri);
  check(
    'resources list panel + illustration template',
    uris.includes('ui://zcode-writing-viewer/panel.html') && uris.some((u) => u.startsWith('ui://zcode-writing-viewer/review-illustration/')),
    uris.join(','),
  );

  const panel = await request('resources/read', { uri: 'ui://zcode-writing-viewer/panel.html' });
  const html = panel.contents?.[0]?.text ?? '';
  check('panel resource serves the mcp-app HTML', panel.contents?.[0]?.mimeType === 'text/html;profile=mcp-app' && html.startsWith('<!doctype html>'), `${html.length} bytes`);

  let illustrationRefused = false;
  try {
    await request('resources/read', { uri: 'ui://zcode-writing-viewer/review-illustration/my-post/fig-1' });
  } catch {
    illustrationRefused = true;
  }
  check('illustration without session fails readably', illustrationRefused);
} catch (error) {
  fails.push(`request failed: ${String(error)}`);
} finally {
  child.stdin.end();
  child.kill();
  // On Windows the child can hold the fixture cwd for a moment after kill,
  // so removal retries briefly before giving up (a leftover temp dir is
  // cosmetic; the shared fixture tree was never touched).
  for (let attempt = 0; attempt < 5; attempt += 1) {
    try {
      rmSync(fixture, { recursive: true, force: true });
      break;
    } catch {
      await new Promise((done) => setTimeout(done, 200));
    }
  }
}

const evidence = resolvePath(root, 'docs', 'evidence', 'server-smoke.txt');
mkdirSync(dirname(evidence), { recursive: true });
writeFileSync(
  evidence,
  `# server smoke（构建产物 dist/server.js，stdio JSON-RPC 驱动）\n# 日期：${new Date().toISOString()}  cwd 夹具：${fixture}\n\n${log.join('\n')}\n\n# 结果：${fails.length === 0 ? '全部通过' : `失败 ${fails.length} 项：${fails.join('、')}`}\n`,
  'utf8',
);
console.log(`smoke: ${fails.length === 0 ? 'PASS' : 'FAIL'}`);
if (fails.length > 0) {
  console.log(fails.join('\n'));
  process.exit(1);
}
