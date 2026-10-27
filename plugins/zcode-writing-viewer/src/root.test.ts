import { describe, expect, it } from 'vitest';
import {
  dedupeCandidates,
  locateWorkspace,
  normalizePathInput,
  workspaceDirOf,
  type RootCandidate,
} from './root';

describe('normalizePathInput', () => {
  it('keeps trimmed non-blank strings', () => {
    expect(normalizePathInput('  /a/b  ')).toBe('/a/b');
  });

  it('rejects non-strings and blank strings as unconfigured', () => {
    expect(normalizePathInput(undefined)).toBeNull();
    expect(normalizePathInput(42)).toBeNull();
    expect(normalizePathInput('   ')).toBeNull();
    expect(normalizePathInput('')).toBeNull();
  });
});

describe('dedupeCandidates', () => {
  it('drops blank and duplicate paths, keeping the first source', () => {
    const candidates: RootCandidate[] = [
      { source: 'config', path: '/proj' },
      { source: 'server-cwd', path: '/proj' },
      { source: 'server-cwd', path: '  ' },
      { source: 'process-cwd', path: '/home' },
    ];
    expect(dedupeCandidates(candidates)).toEqual([
      { source: 'config', path: '/proj' },
      { source: 'process-cwd', path: '/home' },
    ]);
  });
});

describe('workspaceDirOf', () => {
  it('joins the workspace dir name without duplicating trailing slashes', () => {
    expect(workspaceDirOf('/proj')).toBe('/proj/.writing-anything');
    expect(workspaceDirOf('/proj/')).toBe('/proj/.writing-anything');
  });
});

describe('locateWorkspace', () => {
  it('stops at the first candidate whose workspace exists, in priority order', async () => {
    const probed: string[] = [];
    const existing = new Set(['/registry-proj']);
    const result = await locateWorkspace(
      [
        { source: 'config', path: '/config-proj' },
        { source: 'server-cwd', path: '/registry-proj' },
        { source: 'server-cwd', path: '/later-proj' },
        { source: 'process-cwd', path: '/home' },
      ],
      async (root) => {
        probed.push(root);
        return existing.has(root);
      },
    );
    expect(result).toEqual({
      root: '/registry-proj',
      source: 'server-cwd',
      workspaceDir: '/registry-proj/.writing-anything',
      workspaceExists: true,
      candidates: [
        { source: 'config', path: '/config-proj', workspaceExists: false },
        { source: 'server-cwd', path: '/registry-proj', workspaceExists: true },
      ],
    });
    expect(probed).toEqual(['/config-proj', '/registry-proj']);
  });

  it('reports every probe and no root when nothing has a workspace', async () => {
    const result = await locateWorkspace(
      [
        { source: 'config', path: '/a' },
        { source: 'process-cwd', path: '/b' },
      ],
      async () => false,
    );
    expect(result.root).toBeNull();
    expect(result.workspaceExists).toBe(false);
    expect(result.candidates.map((c) => c.path)).toEqual(['/a', '/b']);
  });

  it('treats a rejecting probe as an absent workspace, not a failure', async () => {
    const result = await locateWorkspace(
      [
        { source: 'config', path: '/denied' },
        { source: 'process-cwd', path: '/ok-but-empty' },
      ],
      async (root) => {
        if (root === '/denied') throw new Error('EACCES');
        return false;
      },
    );
    expect(result.root).toBeNull();
    expect(result.candidates).toEqual([
      { source: 'config', path: '/denied', workspaceExists: false },
      { source: 'process-cwd', path: '/ok-but-empty', workspaceExists: false },
    ]);
  });
});
