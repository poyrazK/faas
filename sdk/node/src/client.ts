// Legacy process-wide client. Prefer createClient for independent callers.
import { OpenAPI } from './generated/index.js';
import { asFaasError, isFaasError } from './errors.js';
import type { IdempotencyKey } from './idempotency.js';
import { createHttpTransport, type FaaSClientOptions } from './http-transport.js';
export type { FaaSClientOptions, RetryPolicy, SdkLogger } from './http-transport.js';

/** Read `OpenAPI.HEADERS` as a plain `Record<string, string>`. The
 *  generator types the slot as `Headers | Resolver<Headers> | undefined`
 *  (it accepts an async resolver). The SDK only ever sets a plain
 *  object, so we narrow at the boundary and re-assert on write. */
function readHeaders(): Record<string, string> {
  const h = OpenAPI.HEADERS;
  if (!h) return {};
  if (typeof h === 'function') {
    // Async resolver — we don't support dynamic header resolution
    // (the SDK's contract is "set the bearer token once, mutate
    // headers via setToken/setIdempotencyKey"). Fall through to an
    // empty object; the generator will surface its own error if
    // a resolver was actually required.
    return {};
  }
  return h as Record<string, string>;
}

/**
 * Compatibility façade for static service calls. Configures shared OpenAPI
 * state and installs a process-wide fetch wrapper. Use one per process.
 * @deprecated Use createClient(baseURL, options) and client.apps.getApp(...).
 */
export class FaaSClient {
  private readonly options: FaaSClientOptions;
  private readonly originalFetch: typeof globalThis.fetch;
  private installed = false;

  constructor(baseURL: string, opts: FaaSClientOptions = {}) {
    // Trailing-slash normalisation — the generator's `getUrl` joins
    // `${BASE}${path}` and the paths begin with `/v1/...`. Without
    // the strip, `https://api.example.com/` + `/v1/account` becomes
    // `https://api.example.com//v1/account` which most servers 404.
    OpenAPI.BASE = baseURL.replace(/\/$/, '');
    OpenAPI.TOKEN = opts.token ?? undefined;
    OpenAPI.WITH_CREDENTIALS = false;
    // Resource identifiers stay within one path segment, including organization routes.
    OpenAPI.ENCODE_PATH = encodeURIComponent;
    OpenAPI.HEADERS = opts.token
      ? { Authorization: `Bearer ${opts.token}` }
      : {};
    // Without an explicit key, the wrapper mints once per logical mutating
    // call and preserves that key across all of the call's retry attempts.

    this.options = { ...opts };
    this.originalFetch = opts.fetch ?? globalThis.fetch;

    this.install();
  }

  /** Install the fetch wrapper on globalThis.fetch. Idempotent:
   *  a second call is a no-op. */
  install(): void {
    if (this.installed) return;
    this.installed = true;
    // We replace the global fetch with the wrapped one. Tests that
    // need to swap the underlying transport should pass `fetch` in
    // FaaSClientOptions — that path bypasses the global entirely.
    globalThis.fetch = this.makeWrappedFetch();
  }

  /** Restore globalThis.fetch to its pre-construction value.
   *  Mostly useful for tests; production code rarely calls this. */
  uninstall(): void {
    if (!this.installed) return;
    globalThis.fetch = this.originalFetch;
    this.installed = false;
  }

  /** Rotate the bearer token without rebuilding the client. */
  setToken(token: string | undefined): void {
    OpenAPI.TOKEN = token ?? undefined;
    const headers = readHeaders();
    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    } else {
      delete headers['Authorization'];
    }
    OpenAPI.HEADERS = headers;
  }

  /** Pin a stable Idempotency-Key until explicitly replaced.
   *  Per-request headers take precedence. Use a new key for each independent
   *  operation, and reuse the key when resuming the same logical operation. */
  setIdempotencyKey(key: IdempotencyKey): void {
    const headers = readHeaders();
    headers['Idempotency-Key'] = key;
    OpenAPI.HEADERS = headers;
  }

  /** Build the wrapped fetch: idempotency(rfc7807(retry(logger(userFetch)))). */
  private makeWrappedFetch(): typeof globalThis.fetch {
    return createHttpTransport(
      { ...this.options, fetch: this.originalFetch },
      () => readHeaders()['Idempotency-Key'],
    );
  }

  /** Read the daemon's tx_id off the most recent typed error.
   *  Convenience for support-ticket workflows; equivalent to
   *  `asFaasError(err)?.txId`. */
  static txIdFromError(err: unknown): string | undefined {
    return asFaasError(err)?.txId;
  }

  /** Type guard re-export for convenience. */
  static isFaasError = isFaasError;
}
