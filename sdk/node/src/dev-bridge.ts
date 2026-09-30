import { AsyncLocalStorage } from 'node:async_hooks';

/** Request authority only; laptop attachment credentials never belong here. */
export const DEV_BRIDGE_CONTEXT_HEADER = 'X-Gregale-Dev-Session-Context';
const context = new AsyncLocalStorage<string | undefined>();

function parse(value: string | null | undefined): string | undefined {
  if (!value) return undefined;
  const parts = value.split('.');
  if (parts.length !== 3 || !parts[0] || parts[0].length > 64) return undefined;
  if (!parts.slice(1).every(part => /^[A-Za-z0-9_-]{43}$/.test(part) && Buffer.from(part, 'base64url').length === 32)) return undefined;
  return value;
}

/** Parsing captures context; Gregale still authorizes the session at each hop. */
export function withDevBridgeContext<T>(value: string | null | undefined, handler: () => T): T {
  return context.run(parse(value), handler);
}

export function withDevBridgeRequestContext<T>(headers: HeadersInit, handler: () => T): T {
  return withDevBridgeContext(new Headers(headers).get(DEV_BRIDGE_CONTEXT_HEADER), handler);
}

export function currentDevBridgeContext(): string | undefined {
  return context.getStore();
}

/** Express-compatible middleware, without requiring Express as an SDK dependency. */
export function devBridgeMiddleware(
  request: { headers: Record<string, string | string[] | undefined> },
  _response: unknown,
  next: () => void,
): void {
  const values = Object.entries(request.headers)
    .filter(([name]) => name.toLowerCase() === DEV_BRIDGE_CONTEXT_HEADER.toLowerCase())
    .map(([, value]) => value);
  const value = values.length === 1 && typeof values[0] === 'string' ? values[0] : undefined;
  withDevBridgeContext(value, next);
}

function managed(url: URL): boolean {
  const host = url.hostname.toLowerCase().replace(/\.$/, '');
  return !url.username && !url.password && /^[^.]+\.(?:svc\.gregale|internal)$/.test(host);
}

/**
 * Forward the current request context only to single-label Gregale services.
 * Remove explicit bridge credentials on every destination. Scoped requests use
 * manual redirects so native fetch cannot leak authority to a redirect target.
 * Call this wrapper again when deliberately following a Location response.
 */
export function createDevBridgeFetch(fetchImpl: typeof globalThis.fetch = globalThis.fetch): typeof globalThis.fetch {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request = new Request(input instanceof URL ? input.toString() : input, init);
    const names: string[] = [];
    request.headers.forEach((_value, name) => names.push(name));
    for (const name of names) {
      if (name.toLowerCase().startsWith('x-gregale-dev-bridge-') || name.toLowerCase() === DEV_BRIDGE_CONTEXT_HEADER.toLowerCase()) {
        request.headers.delete(name);
      }
    }
    const value = currentDevBridgeContext();
    if (value && managed(new URL(request.url))) {
      request.headers.set(DEV_BRIDGE_CONTEXT_HEADER, value);
      return fetchImpl(new Request(request, { redirect: 'manual' }));
    }
    return fetchImpl(request);
  };
}
