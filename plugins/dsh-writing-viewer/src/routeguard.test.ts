import { describe, expect, it } from 'vitest';
import { guardRequest, isLoopbackHostname } from './routeguard';

const NO_EXTRAS = { allowedHosts: [], bindHost: null };

describe('isLoopbackHostname', () => {
  it('accepts localhost, the 127/8 range, and ::1 spellings', () => {
    expect(isLoopbackHostname('localhost')).toBe(true);
    expect(isLoopbackHostname('127.0.0.1')).toBe(true);
    expect(isLoopbackHostname('127.199.0.9')).toBe(true);
    expect(isLoopbackHostname('[::1]')).toBe(true);
    expect(isLoopbackHostname('::1')).toBe(true);
  });

  it('refuses anything else', () => {
    expect(isLoopbackHostname('example.net')).toBe(false);
    expect(isLoopbackHostname('128.0.0.1')).toBe(false);
    expect(isLoopbackHostname('127.0.0.999')).toBe(false);
    expect(isLoopbackHostname('::ffff:127.0.0.1')).toBe(false);
    expect(isLoopbackHostname('')).toBe(false);
  });
});

describe('guardRequest host fence', () => {
  it('refuses a missing or unparsable Host header', () => {
    expect(guardRequest({}, NO_EXTRAS).ok).toBe(false);
    expect(guardRequest({ host: '' }, NO_EXTRAS).ok).toBe(false);
    expect(guardRequest({ host: 'http://bad authority' }, NO_EXTRAS).ok).toBe(false);
  });

  it('accepts loopback Host names on any port', () => {
    expect(guardRequest({ host: '127.0.0.1:13080' }, NO_EXTRAS).ok).toBe(true);
    expect(guardRequest({ host: 'localhost' }, NO_EXTRAS).ok).toBe(true);
    expect(guardRequest({ host: '[::1]:13080' }, NO_EXTRAS).ok).toBe(true);
  });

  it('refuses an unknown authority and reads headers case-insensitively', () => {
    const refused = guardRequest({ Host: 'attacker.example' }, NO_EXTRAS);
    expect(refused.ok).toBe(false);
    if (!refused.ok) {
      expect(refused.status).toBe(403);
      expect(refused.reason).toContain('attacker.example');
    }
  });

  it('accepts the bind-address literal regardless of port and zone id', () => {
    const options = { allowedHosts: [] as string[], bindHost: '192.168.1.10' };
    expect(guardRequest({ host: '192.168.1.10:13080' }, options).ok).toBe(true);
    expect(guardRequest({ host: '192.168.1.10' }, options).ok).toBe(true);
    const zoned = { allowedHosts: [] as string[], bindHost: 'fe80::1%eth0' };
    expect(guardRequest({ host: '[fe80::1]:13080' }, zoned).ok).toBe(true);
    // Another address on the same interface is not the bind literal.
    expect(guardRequest({ host: '192.168.1.11' }, options).ok).toBe(false);
  });

  it('matches allowedHosts entries: exact with port, hostname-only on any port', () => {
    const options = { allowedHosts: ['gamepc.example.net:13080', 'proxy.internal'], bindHost: null };
    expect(guardRequest({ host: 'gamepc.example.net:13080' }, options).ok).toBe(true);
    expect(guardRequest({ host: 'proxy.internal:9999' }, options).ok).toBe(true);
    expect(guardRequest({ host: 'proxy.internal' }, options).ok).toBe(true);
    // Wrong port on a port-pinned entry, and unknown hosts, stay refused.
    expect(guardRequest({ host: 'gamepc.example.net:9999' }, options).ok).toBe(false);
    expect(guardRequest({ host: 'other.example.net' }, options).ok).toBe(false);
    // Case never decides trust.
    expect(guardRequest({ host: 'PROXY.internal' }, options).ok).toBe(true);
  });

  it('treats an allowedHosts entry with a redundant default port like the host fence', () => {
    // `host:80` under an HTTP listener normalizes to the bare authority:
    // it matches Host `host` (and `host:80`), never other ports.
    const options = { allowedHosts: ['panel.example.net:80'], bindHost: null };
    expect(guardRequest({ host: 'panel.example.net' }, options).ok).toBe(true);
    expect(guardRequest({ host: 'panel.example.net:80' }, options).ok).toBe(true);
    expect(guardRequest({ host: 'panel.example.net:8080' }, options).ok).toBe(false);
    expect(guardRequest({ host: 'panel.example.net:443' }, options).ok).toBe(false);
  });
});

describe('guardRequest browser markers', () => {
  it('refuses sec-fetch-site: cross-site only', () => {
    const headers = { host: '127.0.0.1:13080' };
    expect(guardRequest({ ...headers, 'sec-fetch-site': 'cross-site' }, NO_EXTRAS).ok).toBe(false);
    expect(guardRequest({ ...headers, 'sec-fetch-site': 'same-origin' }, NO_EXTRAS).ok).toBe(true);
    expect(guardRequest({ ...headers, 'sec-fetch-site': 'same-site' }, NO_EXTRAS).ok).toBe(true);
    expect(guardRequest({ ...headers, 'sec-fetch-site': 'none' }, NO_EXTRAS).ok).toBe(true);
  });

  it('requires an attached Origin to name the same authority as the Host', () => {
    const headers = { host: '127.0.0.1:13080' };
    expect(guardRequest({ ...headers, origin: 'http://127.0.0.1:13080' }, NO_EXTRAS).ok).toBe(true);
    expect(guardRequest({ ...headers, origin: 'http://evil.example' }, NO_EXTRAS).ok).toBe(false);
    expect(guardRequest({ ...headers, origin: 'chrome-extension://abc' }, NO_EXTRAS).ok).toBe(false);
    expect(guardRequest({ ...headers, origin: 'not a url' }, NO_EXTRAS).ok).toBe(false);
    // An empty Origin header is unreadable, not same-origin — refused like the host fence.
    expect(guardRequest({ ...headers, origin: '' }, NO_EXTRAS).ok).toBe(false);
    // Behind an HTTPS proxy the Origin scheme differs from the transport.
    const proxied = { allowedHosts: ['panel.example.net'], bindHost: null };
    expect(guardRequest({ host: 'panel.example.net', origin: 'https://panel.example.net' }, proxied).ok).toBe(true);
  });

  it('never throws on hostile header values', () => {
    expect(guardRequest({ host: '\u0000\uffff' }, NO_EXTRAS).ok).toBe(false);
    expect(guardRequest({ host: '127.0.0.1', origin: '\u0000' }, NO_EXTRAS).ok).toBe(false);
    expect(guardRequest({ host: ['127.0.0.1:13080', 'x'] }, NO_EXTRAS).ok).toBe(true);
  });
});
