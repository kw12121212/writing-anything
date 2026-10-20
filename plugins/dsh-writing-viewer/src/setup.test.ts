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
  prependPathValue,
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
  it('registers one async provider, ranked below every filesystem source, speaking the registry contract', async () => {
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
      expect(candidate.rank).toBeLessThan(100);
      expect(candidate.provider).toBe('dsh-writing-viewer');
      expect(candidate.locator).toBe(candidate.name);
      expect(candidate.invocation).toEqual({ modelInvocable: true, userInvocable: true });
    }
    const definition = await registryGet(provider, candidates[0]!);
    expect(definition?.content).toBe(SKILL_A.body);
    expect(await registryGet(provider, { ...candidates[0]!, name: 'wa-missing' })).toBeUndefined();
    dispose();
    expect(service.providers).toHaveLength(0);
  });
});

describe('prependPathValue and resolveAllOnPath', () => {
  it('prepends once and stays stable afterwards', () => {
    expect(prependPathValue(undefined, '/bin', ':')).toBe('/bin');
    expect(prependPathValue('/usr/bin', '/bin', ':')).toBe('/bin:/usr/bin');
    expect(prependPathValue('/bin:/usr/bin', '/bin', ':')).toBe('/bin:/usr/bin');
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
  it('installs, registers and prepends in one action', async () => {
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
    expect(deps.env.PATH).toBe('/inst:/usr/bin');
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
  it('reports resolution and shadowing against the live PATH', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x', '/usr/bin/wa-cli': 'old' }, envPath: '/usr/bin' });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    // Not enabled yet: resolved still points at the external binary.
    const before = await buildSetupStatus(deps, state);
    expect(before.cli.ready).toBe(true);
    expect(before.cli.resolved).toBe('/usr/bin/wa-cli');
    expect(before.cli.external).toBe('/usr/bin/wa-cli');
    await runSetupAction(deps, state);
    const after = await buildSetupStatus(deps, state);
    expect(after.cli.resolved).toBe('/inst/wa-cli');
    expect(after.cli.external).toBe('/usr/bin/wa-cli');
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

  it('registers and prepends from an existing binary', async () => {
    const deps = makeDeps({ files: { '/inst/wa-cli': 'x' }, fetch: releaseFetch({}, { failAsset: true }) });
    const runner = fullRunner(deps.fs);
    deps.spawn = () => runner;
    const state = createSetupState();
    await startupSetup(deps, state);
    expect(state.registered).toBe(true);
    expect(state.skillNames).toEqual(['wa-auto']);
    expect(deps.env.PATH).toBe('/inst:/usr/bin');
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
  req: { method?: string; headers?: Record<string, unknown> },
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
    expect(deps.env.PATH).toBe('/inst:/usr/bin');
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
