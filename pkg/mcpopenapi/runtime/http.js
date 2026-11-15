import https from 'node:https';
import { lookup } from 'node:dns';
import { isIP } from 'node:net';

export function publicAddress(address) {
  if (isIP(address) === 4) {
    const [a, b, c] = address.split('.').map(Number);
    return a > 0 && a < 224 && a !== 10 && a !== 127
      && !(a === 100 && b >= 64 && b <= 127)
      && !(a === 169 && b === 254)
      && !(a === 172 && b >= 16 && b <= 31)
      && !(a === 192 && (b === 0 && [0, 2].includes(c) || b === 168 || b === 88 && c === 99))
      && !(a === 198 && (b === 18 || b === 19 || b === 51 && c === 100))
      && !(a === 203 && b === 0 && c === 113);
  }
  if (isIP(address) !== 6) return false;
  // Match the generator's ordinary global-IPv6 subset; mapped/translation
  // addresses are excluded rather than reinterpreted by another network stack.
  const first = Number.parseInt(address.split(':')[0], 16);
  const second = Number.parseInt(address.split(':')[1] || '0', 16);
  return first >= 0x2000 && first <= 0x3fff && first !== 0x2002
    && !(first === 0x2001 && (second < 0x0200 || second === 0x0db8))
    && !(first === 0x3fff && second < 0x1000);
}

export function createPublicLookup(resolve = lookup) {
  return (hostname, options, callback) => resolve(hostname, { ...options, all: true }, (error, addresses) => {
    if (error || !addresses.length || addresses.some(entry => !publicAddress(entry.address))) {
      callback(new Error('Public API address required'));
      return;
    }
    if (options.all) callback(null, addresses);
    else callback(null, addresses[0].address, addresses[0].family);
  });
}
export const publicLookup = createPublicLookup();

function assertJSON(value, maxDepth, depth = 0) {
  if (depth > maxDepth) throw new Error('JSON nesting limit');
  if (typeof value === 'number' && (!Number.isFinite(value) || Number.isInteger(value) && !Number.isSafeInteger(value))) {
    throw new Error('Unsafe JSON number');
  }
  if (!value || typeof value !== 'object') return;
  for (const [key, child] of Object.entries(value)) {
    if (['__proto__', 'constructor', 'prototype'].includes(key)) throw new Error('Unsafe JSON property');
    assertJSON(child, maxDepth, depth + 1);
  }
}

export function prepareRequest(manifest, operation, args) {
  if (Buffer.byteLength(JSON.stringify(args)) > manifest.request_max_bytes) throw new Error('Request size limit');
  assertJSON(args, manifest.max_json_depth);
  const segments = operation.path.split('/').map(segment => {
    if (!segment.startsWith('{')) return segment;
    const value = String(args.path[segment.slice(1, -1)]);
    if (!value || value === '.' || value === '..' || /[/\\%\u0000-\u0020]/.test(value)) throw new Error('Invalid path value');
    return encodeURIComponent(value);
  });
  const target = new URL(manifest.server_url + segments.join('/'));
  const base = new URL(manifest.server_url);
  if (target.protocol !== 'https:' || target.origin !== base.origin || target.username || target.password) throw new Error('Target differs from reviewed server');
  for (const parameter of operation.parameters) {
    if (parameter.in === 'query' && args.query?.[parameter.name] !== undefined) {
      target.searchParams.set(parameter.name, String(args.query[parameter.name]));
    }
  }
  if (Buffer.byteLength(target.href) > manifest.url_max_bytes) throw new Error('Request URL size limit');
  const body = args.body === undefined ? undefined : JSON.stringify(args.body);
  return { target, body };
}

export function requestJSON(manifest, operation, args, signal) {
  if (signal?.aborted) return Promise.reject(new Error('API request cancelled'));
  const { target, body } = prepareRequest(manifest, operation, args);
  const hostname = target.hostname.replace(/^\[|\]$/g, '');
  if (isIP(hostname) && !publicAddress(hostname)) return Promise.reject(new Error('Public API address required'));
  return new Promise((resolve, reject) => {
    let settled = false;
    let timer;
    const finish = (error, value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      signal?.removeEventListener('abort', abort);
      if (error) reject(error); else resolve(value);
    };
    const headers = { accept: 'application/json', 'accept-encoding': 'identity' };
    if (body !== undefined) {
      headers['content-type'] = 'application/json';
      headers['content-length'] = String(Buffer.byteLength(body));
    }
    const request = https.request(target, {
      method: operation.method, headers, lookup: publicLookup, agent: false, rejectUnauthorized: true,
    }, response => {
      if (response.statusCode !== operation.response_status
        || typeof response.headers['content-type'] !== 'string'
        || !/^application\/json(?:\s*;|$)/i.test(response.headers['content-type'])) {
        finish(new Error('Unexpected API response'));
        response.destroy();
        return;
      }
      const chunks = [];
      let bytes = 0;
      response.on('data', chunk => {
        bytes += chunk.length;
        if (bytes > manifest.response_max_bytes) {
          finish(new Error('API response size limit'));
          response.destroy();
        } else chunks.push(chunk);
      });
      response.on('error', error => finish(error));
      response.on('aborted', () => finish(new Error('Incomplete API response')));
      response.on('end', () => {
        try {
          const text = new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks));
          const value = JSON.parse(text, (_key, item) => {
            if (typeof item === 'number' && (!Number.isFinite(item) || Number.isInteger(item) && !Number.isSafeInteger(item))) throw new Error('Unsafe JSON number');
            return item;
          });
          assertJSON(value, manifest.max_json_depth);
          finish(null, value);
        } catch (error) { finish(error); }
      });
    });
    const abort = () => request.destroy(new Error('API request cancelled'));
    request.on('error', error => finish(error));
    timer = setTimeout(() => request.destroy(new Error('API request timeout')), manifest.timeout_ms);
    if (signal?.aborted) abort();
    else signal?.addEventListener('abort', abort, { once: true });
    request.end(body);
  });
}
