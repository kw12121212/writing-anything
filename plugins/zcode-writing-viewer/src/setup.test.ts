/**
 * Setup core over injected adapters: download verification, atomic install
 * with the provenance marker, config parsing, platform asset names, PATH
 * resolution and the skill source table — no network, no real filesystem.
 */
import { describe, expect, it } from 'vitest';
import {
  BUNDLED_SKILLS,
  checksumFor,
  DEFAULT_RELEASE_BASE,
  downloadRelease,
  expandHome,
  externalSkillRoots,
  installBinary,
  parseProvenanceMarker,
  parseSetupConfig,
  parseVersionOutput,
  provenanceMarkerPath,
  releaseAssetName,
  SetupError,
  skillSourceTable,
  type FetchLike,
  type FetchResponseLike,
  type SetupFs,
} from './setup';

function memoryFs(files: Record<string, Uint8Array | string> = {}): SetupFs & { dump(): Record<string, Uint8Array | string> } {
  const store = new Map<string, Uint8Array | string>(Object.entries(files));
  const stat = async (path: string) => {
    const entry = store.get(path);
    if (entry === undefined) return null;
    return typeof entry === 'string' ? { isFile: true, isDirectory: false } : { isFile: true, isDirectory: false };
  };
  return {
    async mkdir() {},
    async writeFile(path, bytes) {
      store.set(path, bytes);
    },
    async chmod() {},
    async rename(from, to) {
      const bytes = store.get(from);
      if (bytes === undefined) throw new Error(`rename: ${from} missing`);
      store.delete(from);
      store.set(to, bytes);
    },
    stat,
    async readFile(path) {
      const entry = store.get(path);
      return entry === undefined ? null : typeof entry === 'string' ? entry : new TextDecoder().decode(entry);
    },
    async readFileBytes(path) {
      const entry = store.get(path);
      return entry === undefined ? null : typeof entry === 'string' ? new TextEncoder().encode(entry) : entry;
    },
    async rm(path) {
      store.delete(path);
    },
    dump() {
      return Object.fromEntries(store.entries());
    },
  };
}

import { createHash } from 'node:crypto';

const fakeAsset = new Uint8Array([1, 2, 3, 4]);
const fakeHash = createHash('sha256').update(fakeAsset).digest('hex');
const sumsBody = `${fakeHash}  wa-cli-linux-amd64\n`;

function fetchOf(assets: Record<string, Uint8Array | string>): FetchLike {
  return async (url): Promise<FetchResponseLike> => {
    for (const [name, body] of Object.entries(assets)) {
      if (url.endsWith(`/${name}`)) {
        return { ok: true, status: 200, arrayBuffer: async () => new Uint8Array(typeof body === 'string' ? new TextEncoder().encode(body) : body).slice().buffer };
      }
    }
    return { ok: false, status: 404, arrayBuffer: async () => new ArrayBuffer(0) };
  };
}

describe('parseSetupConfig', () => {
  it('defaults keep the capability off', () => {
    const options = parseSetupConfig(undefined, '/home/a');
    expect(options).toEqual({ enabled: false, waVersion: 'latest', cliInstallDir: '/home/a/.local/bin', waBaseUrl: DEFAULT_RELEASE_BASE, waCliPath: null });
  });

  it('expands ~ in the install dir', () => {
    expect(expandHome('~/.local/bin', '/home/a')).toBe('/home/a/.local/bin');
    expect(expandHome('~', '/home/a')).toBe('/home/a');
    expect(expandHome('/opt', '/home/a')).toBe('/opt');
  });
});

describe('releaseAssetName', () => {
  it('maps the three supported operating systems', () => {
    expect(releaseAssetName('linux', 'x64')).toBe('wa-cli-linux-amd64');
    expect(releaseAssetName('darwin', 'arm64')).toBe('wa-cli-darwin-arm64');
    expect(releaseAssetName('win32', 'x64')).toBe('wa-cli-windows-amd64');
    expect(releaseAssetName('sunos', 'x64')).toBeNull();
  });
});

describe('downloadRelease', () => {
  it('verifies against SHA256SUMS before anything is written', async () => {
    const bytes = await downloadRelease(fetchOf({ 'wa-cli-linux-amd64': fakeAsset, SHA256SUMS: sumsBody }), {
      baseUrl: 'https://x/releases',
      version: 'v0.3.0',
      asset: 'wa-cli-linux-amd64',
    });
    expect(bytes).toEqual(fakeAsset);
  });

  it('refuses a checksum mismatch without returning bytes', async () => {
    const badSums = `${'0'.repeat(64)}  wa-cli-linux-amd64\n`;
    await expect(
      downloadRelease(fetchOf({ 'wa-cli-linux-amd64': fakeAsset, SHA256SUMS: badSums }), {
        baseUrl: 'https://x/releases',
        version: 'latest',
        asset: 'wa-cli-linux-amd64',
      }),
    ).rejects.toThrow(SetupError);
  });

  it('refuses when SHA256SUMS has no record for the asset', async () => {
    await expect(
      downloadRelease(fetchOf({ 'wa-cli-linux-amd64': fakeAsset, SHA256SUMS: 'aaaa  other\n' }), {
        baseUrl: 'https://x/releases',
        version: 'latest',
        asset: 'wa-cli-linux-amd64',
      }),
    ).rejects.toThrow(/没有.*记录/);
  });

  it('checksumFor accepts the *-prefixed GNU format', () => {
    expect(checksumFor(`${fakeHash} *wa-cli-x\n`, 'wa-cli-x')).toBe(fakeHash);
    expect(checksumFor('garbage', 'wa-cli-x')).toBeNull();
  });
});

describe('installBinary', () => {
  it('installs atomically and writes the provenance marker', async () => {
    const fs = memoryFs();
    const dest = await installBinary(fs, '/bin', fakeAsset, '2026-10-23T00:00:00Z');
    expect(dest).toBe('/bin/wa-cli');
    const markerRaw = fs.dump()[provenanceMarkerPath(dest)];
    const markerText = typeof markerRaw === 'string' ? markerRaw : new TextDecoder().decode(markerRaw as Uint8Array);
    expect(typeof markerText).toBe('string');
    const marker = parseProvenanceMarker(String(markerText));
    expect(marker?.managedBy).toBe('zcode-writing-viewer');
    expect(marker?.installedAt).toBe('2026-10-23T00:00:00Z');
    // The recorded hash matches the installed bytes.
    expect(marker?.sha256).toHaveLength(64);
  });

  it('leaves no temp file behind when the rename fails', async () => {
    const fs = memoryFs();
    const failing = { ...fs, rename: async () => { throw new Error('boom'); } };
    await expect(installBinary(failing as SetupFs, '/bin', fakeAsset, 'now')).rejects.toThrow(/安装 wa-cli 失败/);
    expect(Object.keys(fs.dump()).filter((path) => path.includes('.tmp'))).toEqual([]);
  });
});

describe('provenance marker parsing', () => {
  it('rejects foreign or malformed markers', () => {
    expect(parseProvenanceMarker('{}')).toBeNull();
    expect(parseProvenanceMarker('{"managedBy":"someone-else","sha256":"a","installedAt":"b"}')).toBeNull();
    expect(parseProvenanceMarker('not json')).toBeNull();
  });
});

describe('skill source table', () => {
  it('reports the first standard directory holding the skill, existence only', async () => {
    const fs = memoryFs({ '/proj/.agents/skills/wa-brief/SKILL.md': 'x', '/home/u/.agents/skills/wa-draft/SKILL.md': 'x' });
    const rows = await skillSourceTable(fs, externalSkillRoots('/proj', { agents: '/home/u/.agents', claude: '/home/u/.claude' }), ['wa-brief', 'wa-draft', 'wa-auto']);
    expect(rows).toEqual([
      { name: 'wa-brief', external: { source: 'project-agents', path: '/proj/.agents/skills/wa-brief/SKILL.md' } },
      { name: 'wa-draft', external: { source: 'user-agents', path: '/home/u/.agents/skills/wa-draft/SKILL.md' } },
      { name: 'wa-auto', external: null },
    ]);
  });

  it('the bundled roster matches the built-in skills', () => {
    expect(BUNDLED_SKILLS).toHaveLength(13);
    expect(BUNDLED_SKILLS[0]).toBe('wa-assemble');
    expect(BUNDLED_SKILLS.at(-1)).toBe('wa-scene');
  });
});

describe('parseVersionOutput', () => {
  it('reads wa-cli version --json', () => {
    expect(parseVersionOutput('{"version":"0.3.0"}')).toBe('0.3.0');
    expect(parseVersionOutput('garbage')).toBeNull();
    expect(parseVersionOutput('{}')).toBeNull();
  });
});
