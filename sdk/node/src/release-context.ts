import { AsyncLocalStorage } from 'node:async_hooks';

/** Header carrying a deployment-scoped pin for a public app request. */
export const GREGALE_REVISION_HEADER = 'X-Gregale-Revision';

/** Header carrying an immutable project deployment graph. */
export const GREGALE_RELEASE_HEADER = 'X-Gregale-Release';

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

/**
 * Create a fetch function that forwards the current release to managed
 * Gregale service hosts only. It strips X-Gregale-Revision on those hops
 * because that pin belongs to the caller app, not the downstream app. Other
 * hosts and their headers are left untouched.
 *
 * Use it once at app startup and wrap inbound handlers with
 * `withGregaleRequestContext(request.headers, handler)`.
 */
export function createGregaleFetch(fetchImpl: typeof globalThis.fetch = globalThis.fetch): typeof globalThis.fetch {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = urlOf(input);
    if (!url || !isManagedGregaleService(url)) return fetchImpl(input, init);

    const headers = new Headers(input instanceof Request ? input.headers : undefined);
    new Headers(init?.headers).forEach((value, name) => headers.set(name, value));

    // A revision is scoped to the current app and must never escape to a
    // downstream service. The graph release is the only cross-service pin.
    headers.delete(GREGALE_REVISION_HEADER);
    const release = cleanRelease(headers.get(GREGALE_RELEASE_HEADER)) ?? currentGregaleRelease();
    if (release) headers.set(GREGALE_RELEASE_HEADER, release);
    else headers.delete(GREGALE_RELEASE_HEADER);

    return fetchImpl(input, { ...init, headers });
  };
}
