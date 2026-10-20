import { createHash } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import {
  ASSET_MAX_BYTES,
  DEFAULT_INSTALL_DIR,
  DEFAULT_RELEASE_BASE,
  CLI_BINARY_NAME,
  SETUP_SKILL_RANK,
  SetupError,
  checksumFor,
  downloadRelease,
  expandHome,
  installBinary,
  materializeSkills,
  parseSetupConfig,
  parseSkillFile,
  parseVersionOutput,
  appendPathValue,
  releaseAssetName,
  resolveAllOnPath,
  registerSetupSkills,
  type FetchLike,
  type FetchResponseLike,
  type ParsedSkill,
  type SetupFs,
  type SetupRunner,
  type SkillsServiceLike,
  type SkillCandidate,
  externalSkillRoots,
  findExternalSkill,
  PROVENANCE_MARKER_NAME,
  parseProvenanceMarker,
  provenanceMarker,
} from './setup';
import {
  SETUP_PATH,
  builtinCliPath,
  buildSetupStatus,
  createSetupState,
  registerSetupRoutes,
  runSetupAction,
  startupSetup,
  type SetupDeps,
  type SetupState,
} from './setuproutes';
import { apply } from './host';

/** In-memory SetupFs: files as bytes/strings, directories as a set. */
function memoryFs(initial: Record<string, string> = {}) {
  const files = new Map<string, Uint8Array | string>(Object.entries(initial));
  const dirs = new Set<string>(['/']);
  const modes = new Map<string, number>();
  const fs: SetupFs & { paths(): string[]; modesOf(path: string): number | undefined } = {
    async mkdir(dir) {
      for (let path = dir; ; path = path.slice(0, path.lastIndexOf('/'))) {
        dirs.add(path);
        if (path.lastIndexOf('/') <= 0) {
          dirs.add('/');
          break;
        }
      }
    },
    async writeFile(path, bytes) {
      files.set(path, bytes);
    },
    async chmod(path, mode) {
      modes.set(path, mode);
    },
    async rename(from, to) {
      const value = files.get(from);
      if (value === undefined) throw new Error(`no such file: ${from}`);
      files.delete(from);
      files.set(to, value);
      const mode = modes.get(from);
      if (mode !== undefined) {
        modes.delete(from);
        modes.set(to, mode);
      }
    },
    async stat(path) {
      if (files.has(path)) return { isFile: true, isDirectory: false };
      if (dirs.has(path)) return { isFile: false, isDirectory: true };
      return null;
    },
    async readFile(path) {
      const value = files.get(path);
      if (value === undefined) return null;
      return typeof value === 'string' ? value : new TextDecoder().decode(value);
    },
    async readFileBytes(path) {
      const value = files.get(path);
      if (value === undefined) return null;
      return typeof value === 'string' ? new TextEncoder().encode(value) : value;
    },
    async readdir(dir) {
      const prefix = dir.endsWith('/') ? dir : dir + '/';
      const names = new Set<string>();
      for (const path of [...files.keys(), ...dirs]) {
        if (!path.startsWith(prefix)) continue;
        const rest = path.slice(prefix.length);
        if (rest === '') continue;
        names.add(rest.split('/')[0]!);
      }
      return [...names];
    },
    async mkdtemp(prefix) {
      mkdtempCounter += 1;
      const dir = `${prefix}x${mkdtempCounter}`;
      dirs.add(dir);
      return dir;
    },
    async rm(path, recursive) {
      if (files.has(path)) {
        files.delete(path);
        return;
      }
      if (!dirs.has(path)) return;
      if (!recursive) throw new Error('directory not empty');
      const prefix = path.endsWith('/') ? path : path + '/';
      for (const path2 of [...files.keys()]) if (path2.startsWith(prefix)) files.delete(path2);
      for (const path2 of [...dirs]) if (path2.startsWith(prefix)) dirs.delete(path2);
      dirs.delete(path);
    },
    paths() {
      return [...files.keys(), ...dirs].sort();
    },
    modesOf(path) {
      return modes.get(path);
    },
  };
  return fs;
}
let mkdtempCounter = 0;

/** A runner that records every spawn and answers from a (possibly async) handler. */
function fakeRunner(handler: (args: readonly string[], cwd: string) => string | null | Promise<string | null>): SetupRunner & { calls: { args: readonly string[]; cwd: string }[] } {
  const calls: { args: readonly string[]; cwd: string }[] = [];
  return {
    calls,
    async run(args, options) {
      calls.push({ args, cwd: options.cwd });
      const stdout = await handler(args, options.cwd);
      return stdout === null ? null : { stdout };
    },
  };
}

/** A skills service that records registered providers and mirrors how the
 * real registry invokes them: lookups carry an AbortSignal and the registry's
 * waitWithAbort calls `.then` on the provider's return value directly, so a
 * synchronous return is a contract violation the harness below surfaces. */
function fakeSkills() {
  const providers: {
    name: string;
    list(options: { signal?: AbortSignal }): Promise<readonly SkillCandidate[]>;
    get(candidate: SkillCandidate, options: { signal?: AbortSignal }): Promise<{ content: string } | undefined>;
  }[] = [];
  const service: SkillsServiceLike & { providers: typeof providers } = {
    providers,
    registerProvider(create) {
      const provider = create({ signal: new AbortController().signal, invalidate: () => {} });
      providers.push(provider);
      return () => {
        const at = providers.indexOf(provider);
        if (at >= 0) providers.splice(at, 1);
      };
    },
  };
  return service;
}

/** Invoke a provider the way the real registry does under a signal: require a
 * thenable result and await it (waitWithAbort in @deepseek-ai/dsh-skill). */
async function registryList(
  provider: { list(options: { signal?: AbortSignal }): unknown },
): Promise<readonly SkillCandidate[]> {
  const signal = AbortSignal.timeout(5_000);
  const result = provider.list({ signal });
  if (typeof (result as Promise<unknown> | undefined)?.then !== 'function') {
    throw new Error('provider.list 必须返回 Promise：注册表带 signal 调用时会直接对返回值调 .then，同步返回会被整个跳过');
  }
  return (await result) as readonly SkillCandidate[];
}

async function registryGet(
  provider: { get(candidate: SkillCandidate, options: { signal?: AbortSignal }): unknown },
  candidate: SkillCandidate,
): Promise<{ content: string } | undefined> {
  const signal = AbortSignal.timeout(5_000);
  const result = provider.get(candidate, { signal });
  if (typeof (result as Promise<unknown> | undefined)?.then !== 'function') {
    throw new Error('provider.get 必须返回 Promise：注册表带 signal 调用时会直接对返回值调 .then');
  }
  return (await result) as { content: string } | undefined;
}

/** Two skill files shaped like the real built-in SKILL.md layout. */
const SKILL_A: ParsedSkill = { name: 'wa-auto', description: 'Drive the whole flow', body: 'Run wa-cli next.' };
const SKILL_B: ParsedSkill = { name: 'wa-init', description: 'Init a workspace', body: 'Run wa-cli init.' };

function skillMarkdown(skill: ParsedSkill): string {
  return `---\nname: ${skill.name}\ndescription: ${skill.description}\n---\n\n${skill.body}\n`;
}

/** Fetch that serves a release directory from memory with correct checksums. */
function releaseFetch(directory: Record<string, Uint8Array | string>, opts: { failAsset?: boolean } = {}): FetchLike & { urls: string[] } {
  const urls: string[] = [];
  return Object.assign(
    async (url: string): Promise<FetchResponseLike> => {
      urls.push(url);
      const name = url.slice(url.lastIndexOf('/') + 1);
      if (opts.failAsset && name !== 'SHA256SUMS') throw new Error('network unreachable');
      const content = directory[name];
      if (content === undefined) return { ok: false, status: 404, arrayBuffer: async () => new ArrayBuffer(0) };
      const bytes = typeof content === 'string' ? new TextEncoder().encode(content) : content;
      return { ok: true, status: 200, arrayBuffer: async () => bytes.slice().buffer };
    },
    { urls },
  );
}

describe('parseSetupConfig', () => {
  it('stays disabled and on defaults for absent or malformed values', () => {
    const config = parseSetupConfig(undefined, '/home/u');
    expect(config.enabled).toBe(false);
    expect(config.waVersion).toBe('latest');
    expect(config.cliInstallDir).toBe('/home/u/.local/bin');
    expect(config.waBaseUrl).toBe(DEFAULT_RELEASE_BASE);
    expect(parseSetupConfig({ enableSetup: 'yes' }, null).enabled).toBe(false);
    expect(parseSetupConfig({ enableSetup: 1 }, null).enabled).toBe(false);
  });

  it('accepts the switch, a pinned version, a custom dir and base url', () => {
    const config = parseSetupConfig(
      { enableSetup: true, waVersion: 'v0.3.0', cliInstallDir: '~/bin', waBaseUrl: 'https://mirror.example/releases/' },
      '/home/u',
    );
    expect(config.enabled).toBe(true);
    expect(config.waVersion).toBe('v0.3.0');
    expect(config.cliInstallDir).toBe('/home/u/bin');
    expect(config.waBaseUrl).toBe('https://mirror.example/releases');
  });

  it('expands only the owner home forms', () => {
    expect(expandHome('~', '/home/u')).toBe('/home/u');
    expect(expandHome('~/.local/bin', '/home/u')).toBe('/home/u/.local/bin');
    expect(expandHome('~other/bin', '/home/u')).toBe('~other/bin');
    expect(expandHome('~/.local/bin', null)).toBe('~/.local/bin');
    expect(DEFAULT_INSTALL_DIR).toBe('~/.local/bin');
  });
});

describe('releaseAssetName', () => {
  it('maps the supported platforms like install.sh', () => {
    expect(releaseAssetName('linux', 'x64')).toBe('wa-cli-linux-amd64');
    expect(releaseAssetName('darwin', 'arm64')).toBe('wa-cli-darwin-arm64');
  });

  it('returns null for anything else', () => {
    expect(releaseAssetName('win32', 'x64')).toBe(null);
    expect(releaseAssetName('linux', 'riscv64')).toBe(null);
  });
});

describe('checksumFor', () => {
  it('finds the record with or without the binary marker', () => {
    const sums = `${'a'.repeat(64)}  wa-cli-linux-amd64\n${'b'.repeat(64)} *wa-cli-darwin-arm64\r\n`;
    expect(checksumFor(sums, 'wa-cli-linux-amd64')).toBe('a'.repeat(64));
    expect(checksumFor(sums, 'wa-cli-darwin-arm64')).toBe('b'.repeat(64));
    expect(checksumFor(sums, 'wa-cli-linux-arm64')).toBe(null);
  });
});

describe('downloadRelease', () => {
  const asset = 'wa-cli-linux-amd64';
  const bytes = new Uint8Array([1, 2, 3, 4]);
  const sums = `${createHash('sha256').update(bytes).digest('hex')}  ${asset}\n`;

  it('downloads and returns verified bytes', async () => {
    const fetchLike = releaseFetch({ [asset]: bytes, SHA256SUMS: sums });
    const out = await downloadRelease(fetchLike, { baseUrl: 'https://r/releases', version: 'v0.3.0', asset });
    expect([...out]).toEqual([1, 2, 3, 4]);
    expect(fetchLike.urls).toEqual([
      'https://r/releases/download/v0.3.0/wa-cli-linux-amd64',
      'https://r/releases/download/v0.3.0/SHA256SUMS',
    ]);
  });

  it('uses the latest/download directory for the latest version', async () => {
    const fetchLike = releaseFetch({ [asset]: bytes, SHA256SUMS: sums });
    await downloadRelease(fetchLike, { baseUrl: 'https://r/releases', version: 'latest', asset });
    expect(fetchLike.urls[0]).toBe('https://r/releases/latest/download/wa-cli-linux-amd64');
  });

  it('refuses bytes that do not match SHA256SUMS', async () => {
    const tampered = new Uint8Array([9, 9]);
    const fetchLike = releaseFetch({ [asset]: tampered, SHA256SUMS: sums });
    await expect(
      downloadRelease(fetchLike, { baseUrl: 'https://r/releases', version: 'latest', asset }),
    ).rejects.toThrow(/校验失败/);
  });

  it('aborts a streaming body that exceeds the byte cap', async () => {
    // A streaming response whose chunks together exceed ASSET_MAX_BYTES: the
    // reader is cancelled and nothing is buffered past the cap.
    let cancelled = false;
    const streamingResponse = (): FetchResponseLike => ({
      ok: true,
      status: 200,
      body: {
        getReader: () => {
          let sent = 0;
          return {
            read: async () => {
              if (sent >= 3) return { done: true };
              sent += 1;
              return { done: false, value: new Uint8Array(ASSET_MAX_BYTES - 1) };
            },
            cancel: async () => {
              cancelled = true;
            },
          };
        },
      },
      arrayBuffer: () => Promise.reject(new Error('must not fall back')),
    });
    const streamingFetch: FetchLike = Object.assign(async () => streamingResponse(), { urls: [] });
    await expect(
      downloadRelease(streamingFetch, { baseUrl: 'https://r/releases', version: 'latest', asset }),
    ).rejects.toThrow(/超过大小上限/);
    expect(cancelled).toBe(true);
    // A streaming body within the cap is assembled in order.
    const inOrder: FetchResponseLike = {
      ok: true,
      status: 200,
      body: {
        getReader: () => {
          const chunks = [new Uint8Array([1, 2]), new Uint8Array([3])];
          let at = 0;
          return {
            read: async () => (at < chunks.length ? { done: false, value: chunks[at++]! } : { done: true }),
            cancel: async () => undefined,
          };
        },
      },
      arrayBuffer: () => Promise.reject(new Error('must not fall back')),
    };
    const okSums = `${createHash('sha256').update(new Uint8Array([1, 2, 3])).digest('hex')}  ${asset}\n`;
    const okFetch: FetchLike = Object.assign(
      async (url: string) => {
        const name = url.slice(url.lastIndexOf('/') + 1);
        if (name === 'SHA256SUMS') return { ok: true, status: 200, arrayBuffer: async () => new TextEncoder().encode(okSums).slice().buffer };
        return inOrder;
      },
      { urls: [] },
    );
    const bytes = await downloadRelease(okFetch, { baseUrl: 'https://r/releases', version: 'latest', asset });
    expect([...bytes]).toEqual([1, 2, 3]);
  });

  it('reports a missing SHA256SUMS record and network failures readably', async () => {
    const otherOnly = `${'c'.repeat(64)}  some-other-asset\n`;
    await expect(
      downloadRelease(releaseFetch({ [asset]: bytes, SHA256SUMS: otherOnly }), { baseUrl: 'https://r/releases', version: 'latest', asset }),
    ).rejects.toThrow(/SHA256SUMS 中没有/);
    await expect(
      downloadRelease(releaseFetch({ SHA256SUMS: sums }, { failAsset: true }), { baseUrl: 'https://r/releases', version: 'latest', asset }),
    ).rejects.toThrow(/下载失败/);
  });
});

describe('installBinary', () => {
  it('creates the directory and installs atomically with mode 0755', async () => {
    const fs = memoryFs();
    const dest = await installBinary(fs, '/home/u/.local/bin', new Uint8Array([1]));
    expect(dest).toBe(`/home/u/.local/bin/${CLI_BINARY_NAME}`);
    expect(fs.paths()).toContain('/home/u/.local/bin/wa-cli');
    expect(fs.paths().some((path) => path.includes('.tmp'))).toBe(false);
    expect(fs.modesOf(dest)).toBe(0o755);
  });

  it('replaces an existing binary and refuses non-file targets', async () => {
    const fs = memoryFs({ '/bin/wa-cli': 'old' });
    await installBinary(fs, '/bin', new Uint8Array([2]));
    expect(await fs.readFile('/bin/wa-cli')).toBe('\u0002');
    const blocked = memoryFs();
    await blocked.mkdir('/bin/wa-cli');
    await expect(installBinary(blocked, '/bin', new Uint8Array([1]))).rejects.toThrow(/非文件占用/);
  });

  it('cleans up the temp file when the write fails', async () => {
    const fs = memoryFs();
    const original = fs.writeFile;
    fs.writeFile = async (path: string, bytes: Uint8Array) => {
      if (path.includes('.tmp')) throw new Error('disk full');
      await original(path, bytes);
    };
    await expect(installBinary(fs, '/bin', new Uint8Array([1]))).rejects.toThrow(/安装 wa-cli 失败/);
    expect(fs.paths().some((path) => path.includes('.tmp'))).toBe(false);
  });
});

describe('parseSkillFile', () => {
  it('parses frontmatter and body', () => {
    expect(parseSkillFile(skillMarkdown(SKILL_A))).toEqual(SKILL_A);
  });

  it('rejects malformed files', () => {
    expect(parseSkillFile('no frontmatter')).toBe(null);
    expect(parseSkillFile('---\nname: x\n')).toBe(null);
    expect(parseSkillFile('---\nname: x\ndescription: d\n---\n\n')).toBe(null);
    expect(parseSkillFile('---\ndescription: d\n---\n\nbody')).toBe(null);
  });
});

describe('materializeSkills', () => {
  it('spawns skills install inside the temp dir and parses the skills', async () => {
    const fs = memoryFs();
    // The real wa-cli writes the files; emulate that side effect here.
    const writingRunner = fakeRunner(async (args) => {
      if (args[0] === 'skills' && args[1] === 'install') {
        const target = args[3] as string;
        await fs.mkdir(`${target}/${SKILL_A.name}`);
        await fs.writeFile(`${target}/${SKILL_A.name}/SKILL.md`, new TextEncoder().encode(skillMarkdown(SKILL_A)));
        await fs.mkdir(`${target}/${SKILL_B.name}`);
        await fs.writeFile(`${target}/${SKILL_B.name}/SKILL.md`, new TextEncoder().encode(skillMarkdown(SKILL_B)));
        return JSON.stringify({ valid: true, skills: [SKILL_A.name, SKILL_B.name] });
      }
      return null;
    });
    const skills = await materializeSkills(writingRunner, fs, '/tmp');
    expect(skills.map((skill) => skill.name)).toEqual([SKILL_A.name, SKILL_B.name]);
    // The spawn ran inside the temp dir with an argument list, never a shell.
    const call = writingRunner.calls[0]!;
    expect(call.args).toEqual(['skills', 'install', '--target', expect.stringMatching(/^\/tmp\/wa-skills-x1$/), '--json']);
    expect(call.cwd).toMatch(/^\/tmp\/wa-skills-x1$/);
    // The materialization directory is removed afterwards.
    expect(fs.paths().some((path) => path.startsWith('/tmp/wa-skills-'))).toBe(false);
  });

  it('fails readably when the command fails or output is unusable', async () => {
    const fs = memoryFs();
    const nullRunner = fakeRunner(() => null);
    await expect(materializeSkills(nullRunner, fs, '/tmp')).rejects.toThrow(/不可用或执行失败/);
    const badJson = fakeRunner(() => 'not json');
    await expect(materializeSkills(badJson, fs, '/tmp')).rejects.toThrow(/无法解析/);
    const invalid = fakeRunner(() => JSON.stringify({ valid: false, diagnostics: [{ message: 'boom' }] }));
    await expect(materializeSkills(invalid, fs, '/tmp')).rejects.toThrow(/未成功：boom/);
    const empty = fakeRunner(() => JSON.stringify({ valid: true }));
    await expect(materializeSkills(empty, fs, '/tmp')).rejects.toThrow(/没有可用的内置技能/);
  });
});

describe('registerSetupSkills', () => {
  it('registers one async provider, ranked above every filesystem source (fallback), speaking the registry contract', async () => {
    const service = fakeSkills();
    const dispose = registerSetupSkills(service, [SKILL_A, SKILL_B], 'dsh-writing-viewer');
    expect(service.providers).toHaveLength(1);
    const provider = service.providers[0]!;
    expect(provider.name).toBe('dsh-writing-viewer');
    // Drive list/get exactly like the real registry does under a signal: a
    // sync return would throw here and the provider would be skipped.
    const candidates = await registryList(provider);
    expect(candidates.map((candidate) => candidate.name)).toEqual(['wa-auto', 'wa-init']);
    for (const candidate of candidates) {
      expect(candidate.rank).toBe(SETUP_SKILL_RANK);
      // Above every host skill layer (filesystem 100/200/300/400/500, runtime
      // 250, bundled 600): external wins, builtin is the fallback.
      expect(candidate.rank).toBeGreaterThan(600);
      expect(candidate.provider).toBe('dsh-writing-viewer');
      expect(candidate.locator).toBe(candidate.name);
      expect(candidate.invocation).toEqual({ modelInvocable: true, userInvocable: true });
    }
    // Same-name contest, mirroring the registry's ascending comparator: every
    // host layer rank (100/200/300/400/500/600 and runtime 250) sorts ahead
    // of the builtin fallback rank, so an external same-name skill wins.
    const hostLayerRanks = [100, 200, 250, 300, 400, 500, 600];
    for (const externalRank of hostLayerRanks) {
      const ordered = [{ rank: externalRank, provider: 'external' }, { rank: SETUP_SKILL_RANK, provider: 'dsh-writing-viewer' }].sort(
        (left, right) => left.rank - right.rank,
      );
      expect(ordered[0]!.provider).toBe('external');
    }
    const definition = await registryGet(provider, candidates[0]!);
    expect(definition?.content).toBe(SKILL_A.body);
    expect(await registryGet(provider, { ...candidates[0]!, name: 'wa-missing' })).toBeUndefined();
    dispose();
    expect(service.providers).toHaveLength(0);
  });
});

describe('appendPathValue and resolveAllOnPath', () => {
  it('appends at the end once and stays stable wherever the dir sits', () => {
    expect(appendPathValue(undefined, '/inst', ':')).toBe('/inst');
    // Existing order wins: the builtin dir goes last, external stays first.
    expect(appendPathValue('/usr/bin', '/inst', ':')).toBe('/usr/bin:/inst');
    // Present anywhere in PATH (front, middle, end) means no change at all.
    expect(appendPathValue('/inst:/usr/bin', '/inst', ':')).toBe('/inst:/usr/bin');
    expect(appendPathValue('/usr/bin:/inst', '/inst', ':')).toBe('/usr/bin:/inst');
    expect(appendPathValue('/a:/usr/bin:/b:/inst:/c', '/inst', ':')).toBe('/a:/usr/bin:/b:/inst:/c');
  });

  it('lists every PATH entry holding the binary, in order', async () => {
    const fs = memoryFs({ '/old/bin/wa-cli': 'x', '/new/bin/wa-cli': 'y' });
    expect(await resolveAllOnPath(fs, '/new/bin:/old/bin:/empty', CLI_BINARY_NAME, ':')).toEqual([
      '/new/bin/wa-cli',
      '/old/bin/wa-cli',
    ]);
    expect(await resolveAllOnPath(fs, '/empty', CLI_BINARY_NAME, ':')).toEqual([]);
  });
});

describe('parseVersionOutput', () => {
  it('reads the version field and rejects anything else', () => {
    expect(parseVersionOutput('{"valid":true,"version":"v0.3.0"}')).toBe('v0.3.0');
    expect(parseVersionOutput('{}')).toBe(null);
    expect(parseVersionOutput('oops')).toBe(null);
  });
});

/** Assemble SetupDeps over fakes for the route/action tests. */
interface DepsSpec {
  files?: Record<string, string>;
  fetch?: FetchLike;
  skills?: SkillsServiceLike | null;
  runner?: SetupRunner;
  envPath?: string;
  version?: string;
}

function makeDeps(spec: DepsSpec = {}): SetupDeps {
  const fs = memoryFs(spec.files);
  const skills = spec.skills === undefined ? fakeSkills() : spec.skills;
  return {
    options: parseSetupConfig(
      { enableSetup: true, cliInstallDir: '/inst', waVersion: spec.version ?? 'latest' },
      '/home/u',
    ),
    waCliPath: null,
    fs,
    fetchLike: spec.fetch ?? releaseFetch({}),
    spawn: (binary: string) =>
      spec.runner !== undefined ? spec.runner : fakeRunner(() => null),
    skillsService: () => skills,
    env: { PATH: spec.envPath ?? '/usr/bin' },
    pathSeparator: ':',
    tempBase: '/tmp',
    platform: 'linux',
    arch: 'x64',
    now: () => '2026-10-20T00:00:00Z',
    skillHomes: { dsh: '/home/u/.dsh', agents: '/home/u/.agents' },
  };
}

/** A runner that emulates wa-cli fully: version plus skills materialization. */
function fullRunner(fs: SetupFs, version = 'v0.3.0'): SetupRunner {
  return fakeRunner(async (args) => {
    if (args[0] === 'version') return JSON.stringify({ valid: true, version });
    if (args[0] === 'skills' && args[1] === 'install') {
      const target = args[3] as string;
      await fs.mkdir(`${target}/${SKILL_A.name}`);
      await fs.writeFile(`${target}/${SKILL_A.name}/SKILL.md`, new TextEncoder().encode(skillMarkdown(SKILL_A)));
      return JSON.stringify({ valid: true, skills: [SKILL_A.name] });
    }
    return null;
  });
}

describe('runSetupAction', () => {
  it('installs, registers and appends the fallback PATH entry in one action', async () => {
    const asset = 'wa-cli-linux-amd64';
    const binary = new Uint8Array([7, 7]);
    const deps = makeDeps({
      fetch: releaseFetch({ [asset]: binary, SHA256SUMS: `${createHash('sha256').update(binary).digest('hex')}  ${asset}\n` }),
      skills: fakeSkills(),
    });
    const fs = deps.fs;
    const state = createSetupState();
    const runner = fullRunner(fs);
    deps.spawn = (bin: string) => runner;
    const status = await runSetupAction(deps, state);
    expect(status.cli.ready).toBe(true);
    expect(status.cli.path).toBe('/inst/wa-cli');
    expect(status.cli.version).toBe('v0.3.0');
    expect(status.skills.registered).toBe(true);
    expect(status.skills.names).toEqual(['wa-auto']);
    expect(status.lastError).toBe(null);
    expect(deps.env.PATH).toBe('/usr/bin:/inst');
  });

  it('keeps an existing binary with latest and never attempts the download', async () => {
    const fetchLike = releaseFetch({}, { failAsset: true });
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x' }, fetch: fetchLike });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    const status = await runSetupAction(deps, state);
    expect(status.cli.ready).toBe(true);
    expect(status.lastError).toBe(null);
    // No release URL was touched at all — the download step never ran.
    expect(fetchLike.urls).toEqual([]);
  });

  it('re-downloads only when a pinned version differs', async () => {
    const asset = 'wa-cli-linux-amd64';
    const binary = new Uint8Array([7, 7]);
    const sums = `${createHash('sha256').update(binary).digest('hex')}  ${asset}\n`;
    const matching = releaseFetch({ [asset]: binary, SHA256SUMS: sums });
    const depsMatch = makeDeps({ files: { '/inst/wa-cli': 'x' }, fetch: matching, version: 'v0.3.0' });
    const runnerMatch = fullRunner(depsMatch.fs);
    depsMatch.spawn = () => runnerMatch;
    await runSetupAction(depsMatch, createSetupState());
    expect(matching.urls).toEqual([]);
    const stale = releaseFetch({ [asset]: binary, SHA256SUMS: sums });
    const depsStale = makeDeps({ files: { '/inst/wa-cli': 'x' }, fetch: stale, version: 'v0.4.0' });
    const runnerStale = fullRunner(depsStale.fs);
    depsStale.spawn = () => runnerStale;
    const status = await runSetupAction(depsStale, createSetupState());
    expect(status.lastError).toBe(null);
    expect(stale.urls.length).toBe(2);
    expect((depsStale.fs as ReturnType<typeof memoryFs>).paths()).toContain('/inst/wa-cli');
  });

  it('records a readable failure and leaves no partial install', async () => {
    const deps = makeDeps({ fetch: releaseFetch({}, { failAsset: true }) });
    const state = createSetupState();
    const status = await runSetupAction(deps, state);
    expect(status.cli.ready).toBe(false);
    expect(status.lastError).toMatch(/下载失败/);
    expect(status.skills.registered).toBe(false);
    expect((deps.fs as ReturnType<typeof memoryFs>).paths().some((path: string) => path.startsWith('/inst'))).toBe(false);
    // Retry stays possible.
    const again = await runSetupAction(deps, state);
    expect(again.busy).toBe(false);
  });

  it('is single-flight: a second call during an action reports busy only', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x' } });
    let releaser: (() => void) | undefined;
    const gate = new Promise<void>((resolve) => {
      releaser = resolve;
    });
    let runs = 0;
    const slowRunner: SetupRunner = {
      run: async (args, options) => {
        if (args.includes('install')) {
          runs += 1;
          await gate;
        }
        return fullRunner(deps.fs).run(args, options);
      },
    };
    deps.spawn = () => slowRunner;
    const state = createSetupState();
    const first = runSetupAction(deps, state);
    const second = await runSetupAction(deps, state);
    expect(second.busy).toBe(true);
    (releaser as () => void)();
    const done = await first;
    expect(done.busy).toBe(false);
    expect(runs).toBe(1);
  });
});

describe('buildSetupStatus', () => {
  it('reports resolution with external first and builtin as fallback', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x', '/usr/bin/wa-cli': 'old' }, envPath: '/usr/bin' });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    // Not enabled yet: resolved points at the external binary.
    const before = await buildSetupStatus(deps, state);
    expect(before.cli.ready).toBe(true);
    expect(before.cli.resolved).toBe('/usr/bin/wa-cli');
    expect(before.cli.external).toBe('/usr/bin/wa-cli');
    await runSetupAction(deps, state);
    // External-first unchanged after enabling: the builtin dir only appends.
    const after = await buildSetupStatus(deps, state);
    expect(deps.env.PATH).toBe('/usr/bin:/inst');
    expect(after.cli.resolved).toBe('/usr/bin/wa-cli');
    expect(after.cli.external).toBe('/usr/bin/wa-cli');
  });

  it('falls back to the builtin binary when no external wa-cli is on PATH', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x' }, envPath: '/usr/bin' });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    await runSetupAction(deps, state);
    expect(deps.env.PATH).toBe('/usr/bin:/inst');
    const status = await buildSetupStatus(deps, state);
    expect(status.cli.resolved).toBe('/inst/wa-cli');
    expect(status.cli.external).toBe(null);
  });
});

describe('startupSetup', () => {
  it('does nothing without a binary and never touches the network', async () => {
    const deps = makeDeps({ fetch: releaseFetch({}, { failAsset: true }) });
    const state = createSetupState();
    await startupSetup(deps, state);
    expect(state.registered).toBe(false);
    expect(deps.env.PATH).toBe('/usr/bin');
  });

  it('registers and appends the fallback entry from an existing binary', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x' }, fetch: releaseFetch({}, { failAsset: true }) });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    await startupSetup(deps, state);
    expect(state.registered).toBe(true);
    expect(state.skillNames).toEqual(['wa-auto']);
    expect(deps.env.PATH).toBe('/usr/bin:/inst');
  });
});

/** Captured route plus request/response fakes for the handler tests. */
interface CapturedRoute {
  name: string;
  path: string;
  handler(req: { method?: string; headers?: Record<string, unknown>; url?: string }, res: unknown): Promise<void>;
}

function captureScope() {
  const routes: CapturedRoute[] = [];
  const scope = { webServer: { register: (route: unknown) => { routes.push(route as CapturedRoute); } } };
  return { scope, routes };
}

function callRoute(
  route: CapturedRoute,
  req: { method?: string; headers?: Record<string, unknown>; url?: string },
): Promise<{ status: number; headers: Record<string, string>; body: string | undefined }> {
  let captured: { status: number; headers: Record<string, string>; body: string | undefined };
  const res = {
    writeHead(status: number, headers: Record<string, string> = {}) {
      return {
        end(body?: string) {
          captured = { status, headers, body };
        },
      };
    },
    end(body?: string) {
      captured = { status: 200, headers: {}, body };
    },
  };
  return route.handler(req, res).then(() => captured!);
}

const LOOPBACK_HEADERS = { host: '127.0.0.1:13080' };

describe('registerSetupRoutes', () => {
  it('serves the status on GET and runs the action on POST', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x' } });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    const { scope, routes } = captureScope();
    registerSetupRoutes(scope, deps, state, { allowedHosts: [], bindHost: null });
    expect(routes).toHaveLength(1);
    const route = routes[0]!;
    expect(route.path).toBe(SETUP_PATH);
    const statusResponse = await callRoute(route, { method: 'GET', headers: LOOPBACK_HEADERS });
    expect(statusResponse.status).toBe(200);
    expect(JSON.parse(statusResponse.body!).cli.ready).toBe(true);
    const actionResponse = await callRoute(route, { method: 'POST', headers: LOOPBACK_HEADERS });
    const body = JSON.parse(actionResponse.body!);
    expect(body.skills.registered).toBe(true);
    expect(deps.env.PATH).toBe('/usr/bin:/inst');
  });

  it('scopes the source report to a validated root parameter', async () => {
    const deps = makeDeps({
      files: {
        '/inst/wa-cli': 'x',
        '/home/u/.agents/skills/wa-auto/SKILL.md': 'user copy',
        '/proj/.dsh/skills/wa-auto/SKILL.md': 'project copy',
      },
    });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    const { scope, routes } = captureScope();
    registerSetupRoutes(scope, deps, state, { allowedHosts: [], bindHost: null });
    const route = routes[0]!;
    // Register first, then ask with the project root: the project copy wins.
    await callRoute(route, { method: 'POST', headers: LOOPBACK_HEADERS });
    const project = await callRoute(route, { method: 'GET', headers: LOOPBACK_HEADERS, url: '/dsh-writing-viewer/setup?root=/proj' });
    const projectRow = JSON.parse(project.body!).skills.report[0];
    expect(projectRow).toMatchObject({ winner: 'external', source: 'project-dsh', path: '/proj/.dsh/skills/wa-auto/SKILL.md' });
    // Without root the user-level copy wins.
    const user = await callRoute(route, { method: 'GET', headers: LOOPBACK_HEADERS });
    expect(JSON.parse(user.body!).skills.report[0]).toMatchObject({ source: 'user-agents' });
    // An invalid root (relative path) is treated as absent — no unintended
    // probing, the report falls back to the user-level directories.
    const invalid = await callRoute(route, { method: 'GET', headers: LOOPBACK_HEADERS, url: '/dsh-writing-viewer/setup?root=proj/relative' });
    expect(JSON.parse(invalid.body!).skills.report[0]).toMatchObject({ source: 'user-agents' });
  });

  it('rejects forged origins before any pipeline step and 405s other methods', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x' } });
    let spawned = false;
    deps.spawn = () => {
      spawned = true;
      return fakeRunner(() => null);
    };
    const state = createSetupState();
    const { scope, routes } = captureScope();
    registerSetupRoutes(scope, deps, state, { allowedHosts: [], bindHost: null });
    const route = routes[0]!;
    const forged = await callRoute(route, { method: 'POST', headers: { host: 'attacker.example' } });
    expect(forged.status).toBe(403);
    expect(forged.headers['cache-control']).toBe('no-store');
    expect(forged.body).not.toContain('cli');
    const crossSite = await callRoute(route, { method: 'GET', headers: { ...LOOPBACK_HEADERS, 'sec-fetch-site': 'cross-site' } });
    expect(crossSite.status).toBe(403);
    const wrongOrigin = await callRoute(route, { method: 'POST', headers: { ...LOOPBACK_HEADERS, origin: 'http://evil.example' } });
    expect(wrongOrigin.status).toBe(403);
    expect(spawned).toBe(false);
    const method = await callRoute(route, { method: 'PUT', headers: LOOPBACK_HEADERS });
    expect(method.status).toBe(405);
  });
});

describe('apply setup gate', () => {
  interface Injected {
    names: string[];
    closure: (scope: unknown) => void;
  }

  function applyWith(config: Record<string, unknown>): { injected: Injected[]; routes: { name: string; path: string }[] } {
    const injected: Injected[] = [];
    const routes: { name: string; path: string }[] = [];
    apply(
      {
        inject: (names: string[], closure: (scope: unknown) => void) => {
          injected.push({ names, closure });
          return undefined;
        },
      },
      config,
    );
    const webServer = injected.find((entry) => entry.names.includes('webServer'));
    webServer?.closure({
      webServer: {
        register: (route: { name: string; path: string }) => {
          routes.push({ name: route.name, path: route.path });
        },
        host: '127.0.0.1',
      },
    });
    return { injected, routes };
  }

  it('registers only the two read-only routes while the switch is off', () => {
    const { injected, routes } = applyWith({});
    expect(routes.map((route) => route.path)).toEqual(['/dsh-writing-viewer/workspace', '/dsh-writing-viewer/snapshot']);
    expect(injected.some((entry) => entry.names.includes('skills'))).toBe(false);
  });

  it('adds the setup route and the skills seam while the switch is on', () => {
    // cliInstallDir points nowhere, so the startup path stays a no-op and
    // the test never spawns a real binary or touches the real PATH.
    const { injected, routes } = applyWith({ enableSetup: true, cliInstallDir: '/nonexistent-setup-test/bin' });
    expect(routes.map((route) => route.path)).toEqual([
      '/dsh-writing-viewer/workspace',
      '/dsh-writing-viewer/snapshot',
      SETUP_PATH,
    ]);
    expect(injected.some((entry) => entry.names.includes('skills'))).toBe(true);
  });

  it('runs the startup registration once the skills service resolves', async () => {
    const { injected } = applyWith({ enableSetup: true, cliInstallDir: '/nonexistent-setup-test/bin' });
    expect(injected.some((entry) => entry.names.includes('skills'))).toBe(true);
    // Missing binary: resolving the service stays quiet and harmless.
    const skillsInject = injected.find((entry) => entry.names.includes('skills'))!;
    expect(() => skillsInject.closure({ skills: fakeSkills() })).not.toThrow();
  });
});

describe('provenance marker', () => {
  const asset = 'wa-cli-linux-amd64';

  it('rejects malformed or foreign marker payloads', () => {
    const good = JSON.stringify(provenanceMarker(new Uint8Array([1]), '2026-10-20T00:00:00Z'));
    expect(parseProvenanceMarker(good)).not.toBeNull();
    expect(parseProvenanceMarker('not json')).toBeNull();
    expect(parseProvenanceMarker('[]')).toBeNull();
    expect(parseProvenanceMarker(JSON.stringify({ managedBy: 'someone-else', sha256: 'a'.repeat(64), installedAt: 'x' }))).toBeNull();
    expect(parseProvenanceMarker(JSON.stringify({ managedBy: 'dsh-writing-viewer' }))).toBeNull();
    expect(parseProvenanceMarker(JSON.stringify({ managedBy: 'dsh-writing-viewer', sha256: '', installedAt: 'x' }))).toBeNull();
    expect(parseProvenanceMarker(JSON.stringify({ managedBy: 'dsh-writing-viewer', sha256: 'a'.repeat(64), installedAt: 42 }))).toBeNull();
  });

  function releaseFor(bytes: Uint8Array): FetchLike {
    return releaseFetch({ [asset]: bytes, SHA256SUMS: `${createHash('sha256').update(bytes).digest('hex')}  ${asset}\n` });
  }

  it('writes a marker after a plugin install and classifies it plugin-installed', async () => {
    const bytes = new Uint8Array([3, 1, 4]);
    const deps = makeDeps({ fetch: releaseFor(bytes) });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    const status = await runSetupAction(deps, state);
    expect(status.lastError).toBe(null);
    const markerText = await deps.fs.readFile('/inst/wa-cli.dsh-writing-viewer.json');
    expect(markerText).not.toBeNull();
    const marker = JSON.parse(markerText!) as { managedBy: string; sha256: string; installedAt: string };
    expect(marker).toEqual({ managedBy: 'dsh-writing-viewer', sha256: createHash('sha256').update(bytes).digest('hex'), installedAt: '2026-10-20T00:00:00Z' });
    expect(status.cli.pluginInstalled).toBe(true);
  });

  it('classifies an unmarked binary at the same path as not plugin-installed', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x' } });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const status = await runSetupAction(deps, createSetupState());
    expect(status.cli.ready).toBe(true);
    expect(status.cli.pluginInstalled).toBe(false);
    // The same holds for an operator-designated waCliPath binary.
    const configured = makeDeps({ files: { '/opt/wa-cli': 'x' } });
    configured.waCliPath = '/opt/wa-cli';
    const configuredRunner = fullRunner(configured.fs);
    configured.spawn = () => configuredRunner;
    const configuredStatus = await runSetupAction(configured, createSetupState());
    expect(configuredStatus.cli.pluginInstalled).toBe(false);
  });

  it('rejects a stale marker whose sha256 no longer matches the binary', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x' } });
    const stale = JSON.stringify({ managedBy: 'dsh-writing-viewer', sha256: '0'.repeat(64), installedAt: '2026-01-01T00:00:00Z' });
    await deps.fs.writeFile('/inst/wa-cli.dsh-writing-viewer.json', new TextEncoder().encode(stale));
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const status = await runSetupAction(deps, createSetupState());
    expect(status.cli.pluginInstalled).toBe(false);
  });

  it('keeps the install working when the marker write fails', async () => {
    const bytes = new Uint8Array([7]);
    const deps = makeDeps({ fetch: releaseFor(bytes) });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const original = deps.fs.writeFile;
    deps.fs.writeFile = async (path: string, data: Uint8Array) => {
      if (path.endsWith('.dsh-writing-viewer.json')) throw new Error('read-only marker dir');
      await original(path, data);
    };
    const state = createSetupState();
    const status = await runSetupAction(deps, state);
    expect(status.lastError).toBe(null);
    expect(status.cli.ready).toBe(true);
    expect(status.cli.pluginInstalled).toBe(false);
  });
});

describe('skill source report (directory presence)', () => {
  it('orders the standard external roots like the filesystem source', () => {
    expect(externalSkillRoots('/proj', { dsh: '/home/u/.dsh', agents: '/home/u/.agents' })).toEqual([
      { dir: '/proj/.dsh/skills', source: 'project-dsh' },
      { dir: '/proj/.agents/skills', source: 'project-agents' },
      { dir: '/home/u/.dsh/skills', source: 'user-dsh' },
      { dir: '/home/u/.agents/skills', source: 'user-agents' },
    ]);
    expect(externalSkillRoots(null, { dsh: '/home/u/.dsh', agents: '/home/u/.agents' })).toEqual([
      { dir: '/home/u/.dsh/skills', source: 'user-dsh' },
      { dir: '/home/u/.agents/skills', source: 'user-agents' },
    ]);
  });

  it('finds the first external copy in precedence order', async () => {
    const fs = memoryFs({ '/proj/.agents/skills/wa-auto/SKILL.md': 'x', '/home/u/.agents/skills/wa-auto/SKILL.md': 'y' });
    const roots = externalSkillRoots('/proj', { dsh: '/home/u/.dsh', agents: '/home/u/.agents' });
    expect(await findExternalSkill(fs, roots, 'wa-auto')).toEqual({ source: 'project-agents', path: '/proj/.agents/skills/wa-auto/SKILL.md' });
    expect(await findExternalSkill(fs, roots, 'wa-missing')).toBeNull();
  });

  it('reports external winners with paths and builtin otherwise', async () => {
    const deps = makeDeps({
      files: { '/inst/wa-cli': 'x', '/home/u/.agents/skills/wa-auto/SKILL.md': 'external copy' },
    });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    await runSetupAction(deps, state);
    const status = await buildSetupStatus(deps, state);
    expect(status.skills.report).toHaveLength(1);
    expect(status.skills.report![0]).toEqual({ name: 'wa-auto', winner: 'external', source: 'user-agents', path: '/home/u/.agents/skills/wa-auto/SKILL.md', fallbackReady: true });
    // With a project root whose .dsh dir holds a copy, the project wins.
    const depsProject = makeDeps({
      files: {
        '/inst/wa-cli': 'x',
        '/home/u/.agents/skills/wa-auto/SKILL.md': 'user copy',
        '/proj/.dsh/skills/wa-auto/SKILL.md': 'project copy',
      },
    });
    const runnerProject = fullRunner(depsProject.fs);
    depsProject.spawn = () => runnerProject;
    const stateProject = createSetupState();
    await runSetupAction(depsProject, stateProject);
    const projectStatus = await buildSetupStatus(depsProject, stateProject, '/proj');
    expect(projectStatus.skills.report![0]).toEqual({ name: 'wa-auto', winner: 'external', source: 'project-dsh', path: '/proj/.dsh/skills/wa-auto/SKILL.md', fallbackReady: true });
    // No external copy anywhere → builtin; null before registration.
    const depsClean = makeDeps({ files: { '/inst/wa-cli': 'x' } });
    const runnerClean = fullRunner(depsClean.fs);
    depsClean.spawn = () => runnerClean;
    const cleanState = createSetupState();
    expect((await buildSetupStatus(depsClean, cleanState)).skills.report).toBeNull();
    await runSetupAction(depsClean, cleanState);
    expect((await buildSetupStatus(depsClean, cleanState)).skills.report![0]).toEqual({ name: 'wa-auto', winner: 'builtin', source: 'dsh-writing-viewer', path: null, fallbackReady: true });
  });
});