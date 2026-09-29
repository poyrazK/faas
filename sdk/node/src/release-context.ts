import { AsyncLocalStorage } from 'node:async_hooks';

/** Header carrying a deployment-scoped pin for a public app request. */
export const GREGALE_REVISION_HEADER = 'X-Gregale-Revision';

/** Header carrying an immutable project deployment graph. */
export const GREGALE_RELEASE_HEADER = 'X-Gregale-Release';

/** Opaque platform-signed deadline for a participating managed HTTP chain. */
export const GREGALE_REQUEST_DEADLINE_HEADER = 'X-Gregale-Request-Deadline';

const RELEASE_UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const releaseContext = new AsyncLocalStorage<string | undefined>();
const deadlineContext = new AsyncLocalStorage<string | undefined>();

function cleanDeadline(value: string | null | undefined): string | undefined {
  return value && value.length <= 2048 && /^[A-Za-z0-9._-]+$/.test(value) ? value : undefined;
}

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
  const inbound = new Headers(headers);
  return deadlineContext.run(cleanDeadline(inbound.get(GREGALE_REQUEST_DEADLINE_HEADER)), () =>
    withGregaleReleaseContext(inbound.get(GREGALE_RELEASE_HEADER), handler));
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

/** Return the opaque deadline selected for this request. Never edit its bytes. */
export function currentGregaleRequestDeadline(): string | undefined {
  return deadlineContext.getStore();
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
  const host = url.hostname.toLowerCase().replace(/\.$/, '');
  return host.endsWith('.svc.gregale') || host.endsWith('.internal');
}

/**
 * Create a fetch function that forwards the current release to managed
 * Gregale service hosts only. It strips X-Gregale-Revision on those hops
 * because that pin belongs to the caller app, not the downstream app. Deadline
 * carriers are removed from external calls. A signed call returns redirects
 * for the application to follow explicitly through this same helper.
 *
 * Use it once at app startup and wrap inbound handlers with
 * `withGregaleRequestContext(request.headers, handler)`.
 */
export function createGregaleFetch(fetchImpl: typeof globalThis.fetch = globalThis.fetch): typeof globalThis.fetch {
  return async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = urlOf(input);
    if (!url || !isManagedGregaleService(url)) {
      const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
      if (!headers.has(GREGALE_REQUEST_DEADLINE_HEADER)) return fetchImpl(input, init);
      headers.delete(GREGALE_REQUEST_DEADLINE_HEADER);
      return fetchImpl(input, { ...init, headers });
    }

    const headers = new Headers(input instanceof Request ? input.headers : undefined);
    new Headers(init?.headers).forEach((value, name) => headers.set(name, value));

    // A revision is scoped to the current app and must never escape to a
    // downstream service. The graph release is the only cross-service pin.
    headers.delete(GREGALE_REVISION_HEADER);
    const release = cleanRelease(headers.get(GREGALE_RELEASE_HEADER)) ?? currentGregaleRelease();
    if (release) headers.set(GREGALE_RELEASE_HEADER, release);
    else headers.delete(GREGALE_RELEASE_HEADER);

    // Request context wins over an explicit downstream carrier: replacing it
    // would unlink the call from its parent. The gateway verifies the token.
    const deadline = currentGregaleRequestDeadline() ?? cleanDeadline(headers.get(GREGALE_REQUEST_DEADLINE_HEADER));
    if (deadline) headers.set(GREGALE_REQUEST_DEADLINE_HEADER, deadline);
    else headers.delete(GREGALE_REQUEST_DEADLINE_HEADER);

    const redirect = init?.redirect ?? (input instanceof Request ? input.redirect : undefined);
    return fetchImpl(input, { ...init, headers, ...(deadline ? { redirect: redirect === 'error' ? 'error' as const : 'manual' as const } : {}) });
  };
}
