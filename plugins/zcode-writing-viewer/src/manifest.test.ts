/**
 * Manifest shape guard: ZCode validates .zcode-plugin/plugin.json with a
 * strict schema where userConfig entries' `title` and `description` are
 * plain strings (an i18n object there fails the whole marketplace load —
 * the exact failure this file pins). Also pins the surfaces/server/skills
 * wiring the manifest promises.
 */
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const here = new URL('.', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1');
const manifest = JSON.parse(readFileSync(`${here}../.zcode-plugin/plugin.json`, 'utf8')) as {
  name: string;
  version: string;
  skills: string;
  mcpServers: Record<string, { command: string; args: string[]; cwd: string; env: Record<string, string> }>;
  ui: { surfaces: { id: string; server: string; resourceUri: string; availability: string; title: Record<string, string> }[] };
  userConfig: Record<string, { type: string; title: unknown; description: unknown }>;
};

describe('plugin manifest', () => {
  it('userConfig titles and descriptions are plain strings', () => {
    expect(Object.keys(manifest.userConfig).sort()).toEqual([
      'cliInstallDir', 'enableSetup', 'waBaseUrl', 'waCliPath', 'waVersion', 'workspaceRoot',
    ]);
    for (const [key, entry] of Object.entries(manifest.userConfig)) {
      expect(typeof entry.title, `${key}.title`).toBe('string');
      expect(entry.title).not.toBe('');
      expect(typeof entry.description, `${key}.description`).toBe('string');
      expect(entry.description).not.toBe('');
    }
  });

  it('declares the panel surface against its own server', () => {
    const surface = manifest.ui.surfaces[0]!;
    expect(manifest.ui.surfaces).toHaveLength(1);
    expect(surface.id).toBe('writing-workspace');
    expect(surface.server).toBe('zcode-writing-viewer');
    expect(manifest.mcpServers[surface.server]).toBeDefined();
    expect(surface.resourceUri).toBe(`ui://${manifest.name}/panel.html`);
    expect(surface.availability).toBe('session');
    expect(typeof surface.title['zh-CN']).toBe('string');
  });

  it('launches the server with plugin-root paths and userConfig env passing', () => {
    const server = manifest.mcpServers['zcode-writing-viewer']!;
    expect(server.command).toBe('node');
    expect(server.args[0]).toBe('${ZCODE_PLUGIN_ROOT}/dist/server.js');
    expect(server.cwd).toBe('${ZCODE_PROJECT_DIR}');
    for (const key of Object.keys(manifest.userConfig)) {
      const envName = `ZCODE_WV_${key.replace(/([A-Z])/g, '_$1').toUpperCase()}`;
      const value = server.env[envName] ?? '';
      expect(value.includes('${user_config.'), envName).toBe(true);
    }
  });

  it('ships the bundled skills directory', () => {
    expect(manifest.skills).toBe('skills');
  });
});
