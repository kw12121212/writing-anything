/**
 * Request Origin Guard for the writing-viewer host routes.
 *
 * The DSH host webserver calls a registered route's handler directly — there
 * is no authentication middleware a plugin route inherits, and the browser
 * authentication fence lives inside the "/" and "/api" handlers the host
 * owns. This module mirrors the semantics of the host's own /api
 * browser-trust fence (@deepseek-ai/dsh-client-connection, api-request-trust)
 * so plugin routes get the same protection:
 *
 * - the Host header binds every request: over plain HTTP a browser attaches
 *   neither Origin nor Fetch-Metadata to some reads, so an unmarked request
 *   may still be a rebound browser read and Host is the one header DNS
 *   rebinding cannot forge. A request passes when its Host names a loopback
 *   address, the listener's own bind-address literal, or an authority the
 *   plugin config explicitly allows (allowedHosts);
 * - `sec-fetch-site: cross-site` is refused outright;
 * - an attached Origin must name the same authority as the Host.
 *
 * Non-browser tools (curl) send no Origin and no Fetch-Metadata and keep
 * working against loopback exactly as before; the guard decides trust of
 * origins, not identity of users. Everything here is pure and never throws:
 * an unreadable input simply fails the check.
 */

/** Options the guard needs: explicit allowed authorities and the bind host. */
export interface GuardOptions {
  /** Bare `host[:port]` authorities from the plugin config; empty by default. */
  allowedHosts: readonly string[];
  /** The listener's bind address literal (e.g. "127.0.0.1"), when known. */
  bindHost: string | null;
}

/** One guard verdict. Rejects carry a short human-readable reason. */
export type GuardVerdict = { ok: true } | { ok: false; status: 403; reason: string };

/** Normalized URL of a bare authority, or null when unparsable. */
function parseAuthority(authority: string, scheme = 'http:'): URL | null {
  if (typeof URL !== 'function') return null;
  try {
    return new URL(`${scheme}//${authority}`);
  } catch {
    return null;
  }
}

/**
 * Canonical authority of a parsed entry: `hostname` when no explicit port
 * was written, else `hostname:port`. Default ports are judged by parsing
 * under both special schemes (their defaults differ), never from the raw
 * string — the same trick the host fence uses so `host:80` written under an
 * http listener still counts as an explicit port.
 */
function canonicalAuthority(entryUrl: URL, raw: string): { host: string; hasPort: boolean } {
  const otherScheme = entryUrl.protocol === 'http:' ? 'https:' : 'http:';
  let port = entryUrl.port;
  if (port === '') {
    try {
      port = new URL(`${otherScheme}//${raw}`).port;
    } catch {
      port = '';
    }
  }
  return port === '' ? { host: entryUrl.hostname, hasPort: false } : { host: `${entryUrl.hostname}:${port}`, hasPort: true };
}

/** Whether a hostname is a loopback name: localhost, 127.0.0.0/8, or ::1. */
export function isLoopbackHostname(hostname: string): boolean {
  if (hostname === 'localhost' || hostname === '[::1]' || hostname === '::1') return true;
  const unbracketed = hostname.startsWith('[') && hostname.endsWith(']') ? hostname.slice(1, -1) : hostname;
  if (unbracketed === '::1') return true;
  if (hostname.includes(':')) return false;
  const parts = hostname.split('.');
  if (parts.length !== 4 || parts[0] !== '127') return false;
  return parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
}

/** Whether the request Host matches one allowed authority: an entry with an
 * explicit port matches that exact authority, a port-less entry matches the
 * hostname on any port. Both sides compare through WHATWG normalization. */
function matchesAllowedAuthority(hostUrl: URL, allowedHosts: readonly string[]): boolean {
  return allowedHosts.some((entry) => {
    if (typeof entry !== 'string' || entry.trim() === '') return false;
    const entryUrl = parseAuthority(entry);
    if (entryUrl === null) return false;
    const canonical = canonicalAuthority(entryUrl, entry);
    // Like the host fence, an explicit-port entry compares `entryUrl.host`
    // parsed under the listener scheme — so a redundant default port
    // (`host:80` under HTTP) normalizes away and matches that authority,
    // instead of never matching anything.
    if (canonical.hasPort) return entryUrl.host === hostUrl.host;
    return canonical.host === hostUrl.hostname;
  });
}

/** Whether a parsed authority names the deployment's own bind address.
 * Port-insensitive, and a `%zone` id (an interface selector) is ignored. */
function isBindAddressAuthority(hostUrl: URL, bindHost: string | null): boolean {
  if (bindHost === null || bindHost === '') return false;
  const zoneAt = bindHost.indexOf('%');
  const address = zoneAt === -1 ? bindHost : bindHost.slice(0, zoneAt);
  const bracketed = address.includes(':') && !address.startsWith('[') ? `[${address}]` : address;
  const bindUrl = parseAuthority(bracketed);
  return bindUrl !== null && bindUrl.hostname === hostUrl.hostname;
}

/** Whether an Origin header names the same http(s) authority as the Host.
 * Compared under the Origin's scheme so default ports normalize consistently
 * (the transport may sit behind an HTTPS proxy). */
function isSameAuthorityOrigin(origin: string, host: string): boolean {
  let originUrl: URL;
  try {
    originUrl = new URL(origin);
  } catch {
    return false;
  }
  const scheme = originUrl.protocol === 'https:' ? 'https:' : originUrl.protocol === 'http:' ? 'http:' : null;
  if (scheme === null) return false;
  const hostUrl = parseAuthority(host, scheme);
  return hostUrl !== null && originUrl.host === hostUrl.host;
}

/** Read one header value case-insensitively from a Node-style headers record. */
function headerValue(headers: Record<string, unknown>, name: string): string | null {
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() !== name) continue;
    if (typeof value === 'string') return value;
    if (Array.isArray(value) && typeof value[0] === 'string') return value[0];
    return null;
  }
  return null;
}

/**
 * Decide whether one request may reach a plugin route. Pure over the request
 * headers plus the guard options; never throws.
 */
export function guardRequest(headers: Record<string, unknown>, options: GuardOptions): GuardVerdict {
  const host = headerValue(headers, 'host');
  if (host === null || host.trim() === '') return { ok: false, status: 403, reason: '请求缺少 Host 头，已拒绝' };
  const hostUrl = parseAuthority(host);
  if (hostUrl === null) return { ok: false, status: 403, reason: `Host 头无法解析：${host}` };
  if (
    !isLoopbackHostname(hostUrl.hostname) &&
    !matchesAllowedAuthority(hostUrl, options.allowedHosts) &&
    !isBindAddressAuthority(hostUrl, options.bindHost)
  ) {
    return { ok: false, status: 403, reason: `Host 不在允许的来源内：${host}（回环、监听地址或 allowedHosts 之外的权威）` };
  }
  const fetchSite = headerValue(headers, 'sec-fetch-site');
  if (fetchSite === 'cross-site') return { ok: false, status: 403, reason: '请求标记为 cross-site，已拒绝' };
  const origin = headerValue(headers, 'origin');
  // An empty Origin fails same-authority parsing exactly like the host
  // fence: unreadable is not same-origin.
  if (origin !== null && !isSameAuthorityOrigin(origin, host)) {
    return { ok: false, status: 403, reason: `Origin 与 Host 不是同一权威：${origin}` };
  }
  return { ok: true };
}
