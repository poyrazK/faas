// Zero-secret Gregale calls from inside a Gregale VM (ADR-20261122191423).
//
// A service that declares `uses:` grants in gregale.yaml (queue sends,
// realtime publishes, delayed tasks on itself or a sibling app) calls the
// ordinary Gregale API with its instance's gregale:bindings workload-identity
// token instead of an API key. bindingFetch() returns a fetch that adds that
// token, fetched from the loopback FAAS_WORKLOAD_IDENTITY_ENDPOINT and
// refreshed before it expires:
//
//   import { createClient } from '@gregale/sdk';
//   import { bindingFetch } from '@gregale/sdk/preview/bindings/runtime';
//   const gregale = createClient('https://api.gregale.dev', { fetch: bindingFetch() });

export const BINDINGS_AUDIENCE = 'gregale:bindings';
const TOKEN_REFRESH_MARGIN_MS = 60_000;
const DEFAULT_LIFETIME_MS = 300_000;
const MAX_TOKEN_BYTES = 8192;
/** Bounds one token fetch from the loopback endpoint. */
const IDENTITY_TIMEOUT_MS = 10_000;

export interface BindingFetchOptions {
  /** Loopback token endpoint; defaults to FAAS_WORKLOAD_IDENTITY_ENDPOINT. */
  identityEndpoint?: string;
  /** Underlying fetch for both the token endpoint and API calls. */
  fetch?: typeof globalThis.fetch;
  /** Clock for tests. */
  now?: () => number;
}

/** Caches one gregale:bindings token and refreshes it before expiry. */
export class BindingTokenSource {
  private readonly identityURL: URL;
  private readonly fetchImpl: typeof globalThis.fetch;
  private readonly now: () => number;
  private token?: { value: string; refreshAt: number };
  private pending?: Promise<string>;

  constructor(options: BindingFetchOptions = {}) {
    const endpoint = options.identityEndpoint ?? process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT ?? '';
    if (!endpoint || !URL.canParse(endpoint)) {
      throw new Error('No workload identity endpoint (FAAS_WORKLOAD_IDENTITY_ENDPOINT); binding tokens work only inside a Gregale VM');
    }
    this.identityURL = new URL(endpoint);
    if (this.identityURL.protocol !== 'http:' || !['127.0.0.1', '[::1]', 'localhost'].includes(this.identityURL.hostname)) {
      throw new Error('Workload identity endpoint must be loopback HTTP');
    }
    this.identityURL.searchParams.set('audience', BINDINGS_AUDIENCE);
    this.fetchImpl = options.fetch ?? globalThis.fetch;
    this.now = options.now ?? Date.now;
  }

  /** Returns a valid token, fetching a new one when the cached one is due. */
  async get(signal?: AbortSignal): Promise<string> {
    if (this.token && this.now() < this.token.refreshAt) return this.token.value;
    // The load is shared by every concurrent caller, so it takes no caller's
    // signal: one caller aborting must not fail the others.
    this.pending ??= this.load().finally(() => { this.pending = undefined; });
    return abortable(this.pending, signal);
  }

  /** Drops the cached token so the next call fetches a fresh one. */
  invalidate(): void { this.token = undefined; }

  private async load(): Promise<string> {
    const response = await this.fetchImpl(this.identityURL, { signal: AbortSignal.timeout(IDENTITY_TIMEOUT_MS), redirect: 'error', cache: 'no-store' });
    if (!response.ok) throw new Error(`Workload identity unavailable (${response.status})`);
    const text = await response.text();
    if (text.length > 16_384) throw new Error('Invalid workload identity response');
    const body = JSON.parse(text) as { access_token?: unknown; expires_in?: unknown };
    if (typeof body.access_token !== 'string' || !body.access_token || body.access_token.length > MAX_TOKEN_BYTES) {
      throw new Error('Invalid workload identity response');
    }
    const lifetimeMs = typeof body.expires_in === 'number' && body.expires_in > 0 ? body.expires_in * 1000 : DEFAULT_LIFETIME_MS;
    this.token = { value: body.access_token, refreshAt: this.now() + Math.max(lifetimeMs - TOKEN_REFRESH_MARGIN_MS, lifetimeMs / 2) };
    return body.access_token;
  }
}

/**
 * Returns a fetch that authenticates every request with the instance's
 * gregale:bindings token, replacing any Authorization header. A 401 answer
 * drops the cached token and retries once with a fresh one.
 */
export function bindingFetch(options: BindingFetchOptions = {}): typeof globalThis.fetch {
  const source = new BindingTokenSource(options);
  const fetchImpl = options.fetch ?? globalThis.fetch;
  const send = async (input: string | URL | Request, init: RequestInit | undefined): Promise<Response> => {
    const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
    headers.set('Authorization', `Bearer ${await source.get(init?.signal ?? undefined)}`);
    return fetchImpl(input, { ...init, headers });
  };
  return async (input, init) => {
    const response = await send(input, init);
    if (response.status !== 401 || !replayable(input, init)) return response;
    source.invalidate();
    return send(input, init);
  };
}

/**
 * Reports whether a request can be sent again: its body is absent or a
 * string. A stream, or a Request carrying its own body, is consumed by the
 * first send.
 */
function replayable(input: string | URL | Request, init: RequestInit | undefined): boolean {
  if (init?.body !== undefined && init.body !== null) return typeof init.body === 'string';
  return !(input instanceof Request) || input.body === null;
}

/** Settles with promise, or rejects with signal's reason once it aborts. */
function abortable<T>(promise: Promise<T>, signal: AbortSignal | undefined): Promise<T> {
  if (!signal) return promise;
  if (signal.aborted) return Promise.reject(signal.reason);
  return new Promise<T>((resolve, reject) => {
    const onAbort = () => reject(signal.reason);
    signal.addEventListener('abort', onAbort, { once: true });
    promise.then(
      (value) => { signal.removeEventListener('abort', onAbort); resolve(value); },
      (err) => { signal.removeEventListener('abort', onAbort); reject(err); },
    );
  });
}
