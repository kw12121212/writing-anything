/**
 * Real filesystem adapter for the opt-in setup capability — the only place
 * in the plugin package (besides the pure setup core it serves) that touches
 * write-capable node:fs APIs.
 *
 * It backs the download install (temp + rename + chmod into the configured
 * install directory plus the provenance marker). The snapshot/viewer path
 * never goes through this adapter; the read-only static test keeps every
 * other runtime module free of write-capable fs calls and lists exactly this
 * file and the setup core as the declared write surface.
 */
import fsp from 'node:fs/promises';
import type { SetupFs } from './setup';

/** Real SetupFs over node:fs/promises. Only ever touches the configured
 * install directory. */
export function realSetupFs(): SetupFs {
  return {
    async mkdir(dir: string): Promise<void> {
      await fsp.mkdir(dir, { recursive: true });
    },
    async writeFile(path: string, bytes: Uint8Array): Promise<void> {
      await fsp.writeFile(path, bytes);
    },
    async chmod(path: string, mode: number): Promise<void> {
      await fsp.chmod(path, mode);
    },
    async rename(from: string, to: string): Promise<void> {
      await fsp.rename(from, to);
    },
    async stat(path: string): Promise<{ isFile: boolean; isDirectory: boolean } | null> {
      try {
        const info = await fsp.stat(path);
        return { isFile: info.isFile(), isDirectory: info.isDirectory() };
      } catch {
        return null;
      }
    },
    async readFile(path: string): Promise<string | null> {
      try {
        return await fsp.readFile(path, 'utf8');
      } catch {
        return null;
      }
    },
    async readFileBytes(path: string): Promise<Uint8Array | null> {
      try {
        return new Uint8Array(await fsp.readFile(path));
      } catch {
        return null;
      }
    },
    async rm(path: string, recursive: boolean): Promise<void> {
      await fsp.rm(path, { recursive, force: true });
    },
  };
}
