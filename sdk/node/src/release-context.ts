import { AsyncLocalStorage } from 'node:async_hooks';
import { GREGALE_FLAG_PROPAGATION_HEADER, type GregaleFlags } from './flags.js';

/** Header carrying a deployment-scoped pin for a public app request. */
export const GREGALE_REVISION_HEADER = 'X-Gregale-Revision';

/** Header carrying an immutable project deployment graph. */
export const GREGALE_RELEASE_HEADER = 'X-Gregale-Release';

const RELEASE_UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const releaseContext = new AsyncLocalStorage<string | undefined>();

function cleanRelease(value: string | null | undefined): string | undefined {
  const release = value?.trim();
  return release && !/[\s,]/.test(release) ? release : undefined;
}

/**
 * Run a request handler in the release context selected by Gregale. Pass the
 * inbound request's release header; the context is isolated across concurrent
 * async handlers.
 *
 * Revision pins are intentionally not captured here: they are scoped to the
 * app that received them and must not follow an internal service call.
 */
export function withGregaleReleaseContext<T>(release: string | null | undefined, handler: () => T): T {
  return releaseContext.run(cleanRelease(release), handler);
}

/** Capture the header from a Web Request or Headers object and run a handler. */
export function withGregaleRequestContext<T>(headers: HeadersInit, handler: () => T): T {
  return withGregaleReleaseContext(new Headers(headers).get(GREGALE_RELEASE_HEADER), handler);
}

/**
 * Render the selected Gregale release as an HTML meta tag for an SSR page.
 * The value is emitted only when it has the UUID form accepted by the public
 * release-pin API, so untrusted header values cannot inject markup.
 */
export function gregaleReleaseMetaTag(headers: HeadersInit): string {
  const release = cleanRelease(new Headers(headers).get(GREGALE_RELEASE_HEADER));
  if (!release || !RELEASE_UUID_PATTERN.test(release)) return '';
  return `<meta name="gregale-release" content="${release.toLowerCase()}">`;
}

/** Return the current request's release, when one was selected. */
export function currentGregaleRelease(): string | undefined {
  return releaseContext.getStore();
}

function urlOf(input: RequestInfo | URL): URL | undefined {
  try {
    if (input instanceof URL) return input;
    if (input instanceof Request) return new URL(input.url);
    const base = typeof location === 'undefined' ? undefined : location.href;
    return new URL(input, base);
  } catch {
    return undefined;
  }
}

function isManagedGregaleService(url: URL): boolean {
  return url.hostname.toLowerCase().replace(/\.$/, '').endsWith('.svc.gregale');
}

function isRedirectStatus(status: number): boolean {
  return status === 301 || status === 302 || status === 303 || status === 307 || status === 308;
}

const REDIRECT_BODY_HEADERS = ['content-encoding', 'content-language', 'content-length', 'content-location', 'content-type'];
const REDIRECT_CREDENTIAL_HEADERS = ['authorization', 'proxy-authorization', 'cookie', 'cookie2'];

async function fetchWithFlagContext(
  fetchImpl: typeof globalThis.fetch,
  input: RequestInfo | URL,
  init: RequestInit | undefined,
  headers: Headers,
  flagContext: string,
): Promise<Response> {
  const redirectMode = init?.redirect ?? (input instanceof Request ? input.redirect : 'follow');
  if (redirectMode !== 'follow') return fetchImpl(input, { ...init, headers });

  let request = new Request(input, { ...init, headers, redirect: 'manual' });
  let managedChain = true;
  for (let redirects = 0; ; redirects++) {
    const destination = new URL(request.url);
    const requestHeaders = new Headers(request.headers);
    if (managedChain && isManagedGregaleService(destination)) {
      requestHeaders.set(GREGALE_FLAG_PROPAGATION_HEADER, flagContext);
    } else {
      requestHeaders.delete(GREGALE_FLAG_PROPAGATION_HEADER);
      requestHeaders.delete(GREGALE_RELEASE_HEADER);
      managedChain = false;
    }

    const requestForFetch = new Request(request.clone(), { headers: requestHeaders, redirect: 'manual' });
    const response = await fetchImpl(requestForFetch, { redirect: 'manual', headers: requestHeaders });
    if (!isRedirectStatus(response.status)) return response;
    const location = response.headers.get('location');
    if (!location) return response;
    if (redirects >= 20) throw new TypeError('fetch failed: maximum redirect reached');

    const target = new URL(location, destination);
    const nextHeaders = new Headers(requestHeaders);
    if (destination.origin !== target.origin) {
      for (const name of REDIRECT_CREDENTIAL_HEADERS) nextHeaders.delete(name);
    }

    let method = request.method;
    const discardBody = (response.status === 301 || response.status === 302) && method === 'POST'
      || response.status === 303 && method !== 'GET' && method !== 'HEAD';
    if (discardBody) {
      method = 'GET';
      for (const name of REDIRECT_BODY_HEADERS) nextHeaders.delete(name);
      request = new Request(target, {
        method,
        headers: nextHeaders,
        redirect: 'manual',
        signal: request.signal,
        credentials: request.credentials,
        cache: request.cache,
        mode: request.mode,
        referrer: request.referrer,
        referrerPolicy: request.referrerPolicy,
        integrity: request.integrity,
        keepalive: request.keepalive,
      });
    } else {
      request = new Request(target, request.clone());
      const existingHeaderNames: string[] = [];
      request.headers.forEach((_value, name) => existingHeaderNames.push(name));
      for (const name of existingHeaderNames) request.headers.delete(name);
      nextHeaders.forEach((value, name) => request.headers.set(name, value));
    }
  }
}

/**
 * Create a fetch function that forwards the current release and, when opted
 * in, used flag decisions to managed Gregale service hosts only. Flag context
 * is stripped from every outbound origin so an application cannot leak it to
 * an external service.
 *
 * Use it once at app startup and wrap inbound handlers with
 * `withGregaleRequestContext(request.headers, handler)`. Pass the request's
 * GregaleFlags instance to enable flag-decision propagation.
 */
export function createGregaleFetch(
  fetchImpl: typeof globalThis.fetch = globalThis.fetch,
  options: { flags?: Pick<GregaleFlags, 'propagationHeader'> } = {},
): typeof globalThis.fetch {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = urlOf(input);
    const headers = new Headers(input instanceof Request ? input.headers : undefined);
    new Headers(init?.headers).forEach((value, name) => headers.set(name, value));
    headers.delete(GREGALE_FLAG_PROPAGATION_HEADER);
    if (!url || !isManagedGregaleService(url)) return fetchImpl(input, { ...init, headers });

    // A revision is scoped to the current app and must never escape to a
    // downstream service. The graph release is the only cross-service pin.
    headers.delete(GREGALE_REVISION_HEADER);
    const release = cleanRelease(headers.get(GREGALE_RELEASE_HEADER)) ?? currentGregaleRelease();
    if (release) headers.set(GREGALE_RELEASE_HEADER, release);
    else headers.delete(GREGALE_RELEASE_HEADER);
    const flagContext = options.flags?.propagationHeader();
    if (flagContext) {
      headers.set(GREGALE_FLAG_PROPAGATION_HEADER, flagContext);
      return fetchWithFlagContext(fetchImpl, input, init, headers, flagContext);
    }

    return fetchImpl(input, { ...init, headers });
  };
}
