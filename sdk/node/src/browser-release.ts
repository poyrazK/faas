/** Browser-side project-release pinning for Gregale app requests. */

export const GREGALE_REVISION_HEADER = 'X-Gregale-Revision';
export const GREGALE_RELEASE_HEADER = 'X-Gregale-Release';
export const GREGALE_RELEASE_COOKIE = '__Host-gregale_release';

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface GregaleBrowserFetchOptions {
  /**
   * Public Gregale origins this client may pin. Defaults to the current
   * browser origin; add the API origin when the SPA and API are cross-origin.
   * Release context is never added to requests outside this set.
   */
  managedOrigins?: Iterable<string | URL>;
  /** Optional release supplied by an SSR or application bootstrap. */
  initialRelease?: string;
  /** Optional Fetch implementation, primarily useful for tests. */
  fetch?: typeof globalThis.fetch;
  /** Base URL for relative inputs when running outside a browser. */
  baseURL?: string | URL;
}

export interface GregaleBrowserFetchClient {
  /** Fetch wrapper that captures and pins this client instance's release. */
  fetch: typeof globalThis.fetch;
  /** The release captured from the first eligible Gregale response. */
  readonly release: string | undefined;
  /** Explicitly drop the pin, for example when the application reloads. */
  clearRelease(): void;
}

function cleanRelease(value: string | null | undefined): string | undefined {
  const release = value?.trim();
  return release && UUID_PATTERN.test(release) ? release.toLowerCase() : undefined;
}

function readDocumentReleaseCookie(): string | undefined {
  if (typeof document === 'undefined') return undefined;
  try {
    const matches = document.cookie
      .split(';')
      .map((part) => part.trim())
      .filter((part) => part.startsWith(`${GREGALE_RELEASE_COOKIE}=`));
    if (matches.length !== 1) return undefined;
    return cleanRelease(matches[0]?.slice(GREGALE_RELEASE_COOKIE.length + 1));
  } catch {
    return undefined;
  }
}

function clearDocumentReleaseCookie(): void {
  if (typeof document === 'undefined') return;
  try {
    document.cookie = `${GREGALE_RELEASE_COOKIE}=; Path=/; Max-Age=0; Secure; SameSite=Lax`;
  } catch {
    // Cookie access may be unavailable in an opaque or privacy-restricted origin.
  }
}

function normalizeOrigin(value: string | URL): string {
  let url: URL;
  try {
    url = value instanceof URL ? value : new URL(value);
  } catch {
    throw new TypeError(`Gregale managed origin must be an absolute URL: ${String(value)}`);
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new TypeError(`Gregale managed origin must use HTTP or HTTPS: ${url.origin}`);
  }
  return url.origin;
}

function requestURL(input: RequestInfo | URL, baseURL?: string): URL | undefined {
  const value = input instanceof Request ? input.url : input instanceof URL ? input.href : input;
  try {
    return baseURL ? new URL(value, baseURL) : new URL(value);
  } catch {
    // Leave URL validation and the corresponding error to the native fetch.
    return undefined;
  }
}

function requestHeaders(input: RequestInfo | URL, init?: RequestInit): Headers {
  const inherited = input instanceof Request ? input.headers : undefined;
  const headers = new Headers(inherited);
  new Headers(init?.headers).forEach((value, name) => headers.set(name, value));
  return headers;
}

function waitForReleaseDiscovery(discovery: Promise<void>, signal?: AbortSignal | null): Promise<void> {
  if (!signal) return discovery;
  if (signal.aborted) return Promise.reject(signal.reason ?? new DOMException('The operation was aborted.', 'AbortError'));

  return new Promise<void>((resolve, reject) => {
    const onAbort = () => {
      signal.removeEventListener('abort', onAbort);
      reject(signal.reason ?? new DOMException('The operation was aborted.', 'AbortError'));
    };
    signal.addEventListener('abort', onAbort, { once: true });
    if (signal.aborted) {
      onAbort();
      return;
    }
    discovery.then(
      () => {
        signal.removeEventListener('abort', onAbort);
        resolve();
      },
      (error: unknown) => {
        signal.removeEventListener('abort', onAbort);
        reject(error);
      },
    );
  });
}

/**
 * Create a browser-safe fetch wrapper that learns a Gregale project release
 * from a response and pins subsequent requests to that release.
 *
 * State is scoped to this client instance and stays in memory for the page
 * lifetime. The wrapper returns 410 responses unchanged; it never silently
 * falls back to the active release when the captured release expires.
 */
export function createGregaleBrowserFetch(options: GregaleBrowserFetchOptions = {}): GregaleBrowserFetchClient {
  const browserLocation = typeof globalThis.location === 'undefined' ? undefined : globalThis.location;
  const configuredOrigins = options.managedOrigins ?? (browserLocation ? [browserLocation.origin] : undefined);
  if (!configuredOrigins) {
    throw new TypeError('managedOrigins is required when createGregaleBrowserFetch runs outside a browser');
  }

  const managedOrigins = new Set(Array.from(configuredOrigins, normalizeOrigin));
  const baseURL = options.baseURL instanceof URL ? options.baseURL.href : options.baseURL ?? browserLocation?.href;
  const fetchImpl = options.fetch ?? globalThis.fetch;
  if (typeof fetchImpl !== 'function') {
    throw new TypeError('a Fetch implementation is required');
  }

  let currentRelease = options.initialRelease === undefined
    ? readDocumentReleaseCookie()
    : cleanRelease(options.initialRelease);
  if (options.initialRelease !== undefined && currentRelease === undefined) {
    throw new TypeError('initialRelease must be a Gregale release UUID');
  }
  let releaseDiscovery: Promise<void> | undefined;
  let releaseGeneration = 0;

  const wrappedFetch: typeof globalThis.fetch = async (input, init) => {
    const url = requestURL(input, baseURL);
    if (!url || !managedOrigins.has(url.origin)) {
      return fetchImpl(input, init);
    }

    const headers = requestHeaders(input, init);
    const hasExplicitPin = headers.has(GREGALE_REVISION_HEADER) || headers.has(GREGALE_RELEASE_HEADER);
    // An exact deployment pin targets this app and cannot be combined with
    // a project release. Preserve explicit caller headers in either case.
    if (!hasExplicitPin && currentRelease) {
      headers.set(GREGALE_RELEASE_HEADER, currentRelease);
    }
    const requestInit: RequestInit = { ...init, headers };

    // Until the first unpinned managed response selects a release, hold other
    // unpinned calls so concurrent SPA startup requests cannot land on
    // different active release sets during a cutover.
    if (hasExplicitPin || currentRelease) {
      return fetchImpl(input, requestInit);
    }

    if (releaseDiscovery) {
      const signal = init?.signal === undefined && input instanceof Request ? input.signal : init?.signal;
      await waitForReleaseDiscovery(releaseDiscovery, signal);
      if (!headers.has(GREGALE_REVISION_HEADER) && !headers.has(GREGALE_RELEASE_HEADER) && currentRelease) {
        headers.set(GREGALE_RELEASE_HEADER, currentRelease);
      }
      return fetchImpl(input, requestInit);
    }

    let finishDiscovery!: () => void;
    const discovery = new Promise<void>((resolve) => {
      finishDiscovery = resolve;
    });
    const generation = releaseGeneration;
    releaseDiscovery = discovery;
    try {
      const response = await fetchImpl(input, requestInit);
      if (releaseGeneration === generation) {
        currentRelease = cleanRelease(response.headers.get(GREGALE_RELEASE_HEADER));
      }
      return response;
    } finally {
      if (releaseDiscovery === discovery) releaseDiscovery = undefined;
      finishDiscovery();
    }
  };

  return {
    fetch: wrappedFetch,
    get release() {
      return currentRelease;
    },
    clearRelease() {
      releaseGeneration += 1;
      releaseDiscovery = undefined;
      currentRelease = undefined;
      clearDocumentReleaseCookie();
    },
  };
}
