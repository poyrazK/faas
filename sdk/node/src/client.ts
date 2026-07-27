// src/client.ts — FaaSClient façade + fetch wrapper stack.
//
// The contract mirrors `pkg/api/client.go::NewClient` and
// `sdk/go/client.go::NewClient`:
//
//   * A single `FaaSClient` instance owns its base URL, bearer token,
//     idempotency key, retry policy, and logger.
//   * The wrapper stack is installed on `globalThis.fetch` because
//     openapi-typescript-codegen@0.31.0 calls `fetch(url, request)`
//     directly (no `OpenAPI.fetch` seam). This is the documented seam
//     for the generator — see `src/generated/core/request.ts:219`.
//   * Stack ordering (outermost → innermost), matching the Go SDK's
//     RoundTripper chain at `sdk/go/client.go:74-79`:
//       1. retry      — bounded exponential backoff on 5xx + 429
//       2. rfc7807    — decode Problem bodies, raise typed errors
//       3. logger     — emit one log line per attempt
//       4. idempotency — auto-mint Idempotency-Key on mutating calls
//       5. user fetch — globalThis.fetch (or a test-supplied fetch)
//
// Why the stack installs on globalThis.fetch instead of monkey-
// patching the generator: the generator ships 16 service files that
// each call `request()` which calls `sendRequest()` which calls the
// module-level `fetch()`. There is no `OpenAPI.fetch` field; the
// only non-fork seam is to wrap the global. The wrapper is installed
// once per `FaaSClient` instance and `uninstall()` is exported for
// tests that need to swap the underlying transport.

import { OpenAPI } from './generated/index.js';
import {
  asFaasError,
  isFaasError,
  problemToError,
  type FaasError,
  type Problem,
} from './errors.js';
import { isMutating, mintIdempotencyKey, type IdempotencyKey } from './idempotency.js';

/** Minimal logger contract — the SDK doesn't bind to `console` or a
 *  third-party logger. Customers can pass `console`, pino, winston,
 *  or any object with the four methods below. Methods may be no-ops. */
export interface SdkLogger {
  debug: (...args: unknown[]) => void;
  info: (...args: unknown[]) => void;
  warn: (...args: unknown[]) => void;
  error: (...args: unknown[]) => void;
}

/** No-op logger used when the caller doesn't supply one. */
const noopLogger: SdkLogger = {
  debug: () => {},
  info: () => {},
  warn: () => {},
  error: () => {},
};

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

/** Retry policy. `maxAttempts` is the TOTAL number of attempts
 *  (initial + retries); `maxAttempts:1` disables retry. */
export interface RetryPolicy {
  maxAttempts: number;
  backoffMs: number;
}

/** Options for `new FaaSClient(...)`. */
export interface FaaSClientOptions {
  /** Bearer API key. Pass an empty string or omit for anonymous
   *  endpoints (the smoke fixture is permissive; real apid rejects
   *  missing/invalid Authorization). */
  token?: string;
  /** Optional logger. Defaults to no-op. */
  logger?: SdkLogger;
  /** Optional retry policy. Default = `{maxAttempts:1, backoffMs:0}`
   *  which is a no-op identity (matches Go SDK's `WithRetry` default
   *  of `max<=0` short-circuiting to the inner Transport). */
  retry?: RetryPolicy;
  /** Optional request timeout in milliseconds. Passed straight
   *  through to the wrapped fetch via `AbortSignal.timeout`. */
  timeoutMs?: number;
  /** Optional override for the underlying fetch. Used by tests to
   *  inject a stub. Production callers leave this unset. */
  fetch?: typeof globalThis.fetch;
}

/**
 * Public façade. Owns the global fetch wrapper; customers call
 * generated service methods (`AppsService.listApps()`) which read
 * `OpenAPI.BASE/TOKEN/HEADERS` set at construction time.
 *
 * Lifecycle: a FaaSClient is single-tenant. Create one per process
 * (typical usage: module-level singleton). To rotate the token at
 * runtime, call `setToken()`; to rebind the base URL, call
 * `setBaseURL()`. The wrapper survives both.
 */
export class FaaSClient {
  private readonly logger: SdkLogger;
  private readonly retry: RetryPolicy;
  private readonly timeoutMs: number | undefined;
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
    OpenAPI.HEADERS = opts.token
      ? { Authorization: `Bearer ${opts.token}` }
      : {};
    // No default Idempotency-Key here. The wrapper's idempotencyLayer
    // mints a fresh UUIDv4 per attempt at request time, so a value
    // stashed at construction time would be wasted — the very first
    // mutating attempt would overwrite it anyway.

    this.logger = opts.logger ?? noopLogger;
    this.retry = opts.retry ?? { maxAttempts: 1, backoffMs: 0 };
    this.timeoutMs = opts.timeoutMs;
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

  /** Pin a stable Idempotency-Key for the next mutating call.
   *  Per-call keys override the auto-mint; the wrapper reads the
   *  HEADERS slot at send time and replaces it with a fresh UUIDv4
   *  after a successful response. */
  setIdempotencyKey(key: IdempotencyKey): void {
    const headers = readHeaders();
    headers['Idempotency-Key'] = key;
    OpenAPI.HEADERS = headers;
  }

  /** Build the wrapped fetch. Exported via `install()` above; the
   *  composition is: retry(rfc7807(logger(idempotency(userFetch)))). */
  private makeWrappedFetch(): typeof globalThis.fetch {
    const userFetch = this.originalFetch;
    const retry = this.retry;
    const logger = this.logger;
    const timeoutMs = this.timeoutMs;

    // idempotency layer — innermost of the four wrappers.
    const idempotencyLayer: typeof globalThis.fetch = async (input, init) => {
      // `init` is `RequestInit | undefined`. We mutate a copy so
      // the caller's headers object is not touched (cf. Go SDK's
      // "Body rewind" pattern — the request is single-use on retry).
      const reqInit = init ? { ...init } : {};
      const headers = new Headers(reqInit.headers ?? {});
      const method = (reqInit.method ?? 'GET').toUpperCase();
      if (isMutating(method) && !headers.has('Idempotency-Key')) {
        // Read the client-wide default, but stamp a fresh key per
        // attempt (the server's 24h replay window keys on the
        // header value — a fresh UUID means a fresh retry budget).
        const defaultKey = readHeaders()['Idempotency-Key'];
        headers.set('Idempotency-Key', defaultKey ?? mintIdempotencyKey());
      }
      reqInit.headers = headers;
      return userFetch(input, reqInit);
    };

    // logger layer — one log line per attempt.
    const loggerLayer: typeof globalThis.fetch = async (input, init) => {
      const start = Date.now();
      const url = typeof input === 'string'
        ? input
        : input instanceof URL ? input.toString() : input.url;
      logger.debug('faas http request', { method: init?.method ?? 'GET', url });
      try {
        const resp = await idempotencyLayer(input, init);
        const elapsed = Date.now() - start;
        logger.debug('faas http response', {
          method: init?.method ?? 'GET',
          url,
          status: resp.status,
          elapsed_ms: elapsed,
        });
        return resp;
      } catch (err) {
        const elapsed = Date.now() - start;
        // Use the structured toString() so log lines carry the
        // canonical Problem.code (e.g. `not_found`) instead of the
        // generic `Error: <message>` shape. Falls back to String(err)
        // for non-FaasError throws (network errors, AbortError).
        let errorText: string;
        if (isFaasError(err)) {
          errorText = `${err.problem.code ?? 'unknown'}: ${err.toString()}`;
        } else {
          errorText = String(err);
        }
        logger.debug('faas http response (error)', {
          method: init?.method ?? 'GET',
          url,
          elapsed_ms: elapsed,
          error: errorText,
        });
        throw err;
      }
    };

    // rfc7807 layer — decode Problem bodies, raise typed errors.
    const rfc7807Layer: typeof globalThis.fetch = async (input, init) => {
      const resp = await loggerLayer(input, init);
      if (resp.status >= 400) {
        // Decode Problem bodies and raise typed sentinels. We
        // throw here, so the generator's `request.ts` never reads
        // the body — no defensive clone needed. (Earlier revisions
        // cloned the body to guard against a code path that doesn't
        // exist; the throw short-circuits the generator's parsing.)
        const ct = resp.headers.get('content-type') ?? '';
        if (ct.includes('application/problem+json') || ct.includes('application/json')) {
          let body: unknown = null;
          try {
            body = await resp.json();
          } catch {
            // Malformed body — fall through to status-based error.
          }
          const problem = (body && typeof body === 'object' ? body : {}) as Problem;
          const err = problemToError(problem, resp.status);
          // If the wrapper already produced a typed error, throw it.
          // The generator's `catchErrorCodes` would otherwise raise
          // its own `ApiError`; we want customers to see our typed
          // sentinels.
          throw err satisfies FaasError;
        }
        // Non-JSON error body — return as-is so the generator's
        // own ApiError path takes over. (Authlimiter returns
        // text/plain; that's the canonical case for "no Problem".)
      }
      return resp;
    };

    // retry layer — outermost; sees every attempt.
    const retryLayer: typeof globalThis.fetch = async (input, init) => {
      if (retry.maxAttempts <= 1) {
        // No-op identity.
        return rfc7807Layer(input, init);
      }
      const baseBackoff = retry.backoffMs;
      let lastErr: unknown;
      let lastResp: Response | undefined;
      // Subscribe to caller cancellation ONCE. The signal is a
      // one-shot event source; we listen at retry start so the
      // very first sleep can abort immediately. The listener
      // outlives the loop — `ac.signal` would otherwise fail to
      // fire on the first attempt if the caller aborts before
      // we entered the generator.
      const signal = init?.signal;
      let cancelReason: unknown = undefined;
      const onAbort = () => { cancelReason = signal?.reason ?? new Error('aborted'); };
      if (signal) {
        if (signal.aborted) {
          throw signal.reason ?? new Error('aborted');
        }
        signal.addEventListener('abort', onAbort, { once: true });
      }
      try {
        for (let attempt = 0; attempt < retry.maxAttempts; attempt++) {
          if (attempt > 0) {
            const delay = baseBackoff * Math.pow(2, attempt - 1);
            // Sleep with mid-sleep cancellation. We rasterize the
            // sleep into 50ms ticks so a caller abort at T+0.05
            // terminates within 50ms, not at the full `delay`.
            // 50ms is the same resolution the Go SDK uses
            // (pkg/api/sse.go::Decoder) so behaviour is consistent
            // across SDKs.
            const tickMs = 50;
            let slept = 0;
            while (slept < delay) {
              if (cancelReason !== undefined) break;
              const step = Math.min(tickMs, delay - slept);
              await new Promise<void>((resolve) => setTimeout(resolve, step));
              slept += step;
            }
            if (cancelReason !== undefined) throw cancelReason;
          }
          try {
            const resp = await rfc7807Layer(input, init);
            if (resp.status < 500 && resp.status !== 429) {
              return resp;
            }
            // Drain the body so the connection can be reused.
            try { await resp.arrayBuffer(); } catch { /* ignore */ }
            lastResp = resp;
          } catch (err) {
            // rfc7807Layer threw a typed FaasError — surface immediately.
            // Network errors bubble up the same way (the SDK caller
            // owns cancellation via context.AbortSignal).
            throw err;
          }
        }
      } finally {
        if (signal) signal.removeEventListener('abort', onAbort);
      }
      // Budget exhausted — return the last response so the generator
      // surfaces it via its own error path, OR rethrow the last error.
      if (lastErr) throw lastErr;
      if (lastResp) return lastResp;
      // Should be unreachable.
      throw new Error('retry: budget exhausted without response');
    };

    // Wrap the whole stack with the caller's optional timeout.
    if (timeoutMs && timeoutMs > 0) {
      return async (input, init) => {
        const signal = init?.signal
          ? AbortSignal.any([init.signal, AbortSignal.timeout(timeoutMs)])
          : AbortSignal.timeout(timeoutMs);
        return retryLayer(input, { ...init, signal });
      };
    }
    return retryLayer;
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