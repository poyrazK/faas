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
//   * Stack ordering (outermost → innermost):
//       1. idempotency — one Idempotency-Key per logical mutating call
//       2. rfc7807    — decode the final Problem, raise typed errors
//       3. retry      — bounded exponential backoff on 5xx + 429
//       4. logger     — emit one log line per attempt
//       5. user fetch — globalThis.fetch (or a test-supplied fetch)
//
// Why the stack installs on globalThis.fetch instead of monkey-
// patching the generator: the generator ships 16 service files that
// each call `request()` which calls `sendRequest()` which calls the
// module-level `fetch()`. There is no `OpenAPI.fetch` field; the
// only non-fork seam is to wrap the global. The wrapper is installed
// once per `FaaSClient` instance and `uninstall()` is exported for
// tests that need to swap the underlying transport.

import timers from 'node:timers/promises';
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

/** Retry-After accepts delta seconds or an HTTP date. Invalid values use the policy backoff. */
function retryAfterMs(value: string | null): number {
  if (!value) return 0;
  const raw = value.trim();
  const delay = /^\d+$/.test(raw) ? Number(raw) * 1000 : Date.parse(raw) - Date.now();
  return Number.isFinite(delay) ? Math.max(0, delay) : 0;
}

async function waitForRetry(delayMs: number, signal?: AbortSignal | null): Promise<void> {
  signal?.throwIfAborted();
  // Node clamps timers above this limit to 1 ms. Split long Retry-After waits
  // so they remain cancellable and never send an early retry.
  while (delayMs > 0) {
    const chunk = Math.min(delayMs, 2 ** 31 - 1);
    try {
      await timers.setTimeout(chunk, undefined, { signal: signal ?? undefined });
    } catch (error) {
      signal?.throwIfAborted();
      throw error;
    }
    delayMs -= chunk;
  }
}

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
    // Resource identifiers stay within one path segment, including organization routes.
    OpenAPI.ENCODE_PATH = encodeURIComponent;
    OpenAPI.HEADERS = opts.token
      ? { Authorization: `Bearer ${opts.token}` }
      : {};
    // Without an explicit key, the wrapper mints once per logical mutating
    // call and preserves that key across all of the call's retry attempts.

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
    const userFetch = this.originalFetch;
    const retry = this.retry;
    const logger = this.logger;
    const timeoutMs = this.timeoutMs;

    // Prepare request identity once, outside the per-attempt transport.
    const idempotencyLayer: typeof globalThis.fetch = async (input, init) => {
      const reqInit = init ? { ...init } : {};
      const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
      const method = (reqInit.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
      reqInit.method = method;
      if (reqInit.signal === undefined && input instanceof Request) reqInit.signal = input.signal;
      if (isMutating(method) && !headers.has('Idempotency-Key')) {
        const defaultKey = readHeaders()['Idempotency-Key'];
        headers.set('Idempotency-Key', defaultKey ?? mintIdempotencyKey());
      }
      reqInit.headers = headers;
      if (timeoutMs && timeoutMs > 0) {
        const timeout = AbortSignal.timeout(timeoutMs);
        reqInit.signal = reqInit.signal ? AbortSignal.any([reqInit.signal, timeout]) : timeout;
      }
      return rfc7807Layer(input, reqInit);
    };

    // logger layer — one log line per attempt.
    const loggerLayer: typeof globalThis.fetch = async (input, init) => {
      const start = Date.now();
      const url = typeof input === 'string'
        ? input
        : input instanceof URL ? input.toString() : input.url;
      logger.debug('faas http request', { method: init?.method ?? 'GET', url });
      try {
        const resp = await userFetch(input, init);
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
      const resp = await retryLayer(input, init);
      init?.signal?.throwIfAborted();
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
            init?.signal?.throwIfAborted();
            // Malformed body — fall through to status-based error.
          }
          const problem = (body && typeof body === 'object' ? body : {}) as Problem;
          const err = problemToError(problem, resp.status);
          const retryAfter = resp.headers.get('Retry-After');
          if (retryAfter) Object.defineProperty(err, 'retryAfter', { value: retryAfter, enumerable: true });
          // If the wrapper already produced a typed error, throw it.
          // The generator's `catchErrorCodes` would otherwise raise
          // its own `ApiError`; we want customers to see our typed
          // sentinels.
          throw err satisfies FaasError;
        }
        const retryAfter = resp.headers.get('Retry-After');
        if (retryAfter) {
          await resp.body?.cancel().catch(() => undefined);
          const err = problemToError({}, resp.status);
          Object.defineProperty(err, 'retryAfter', { value: retryAfter, enumerable: true });
          throw err;
        }
        // Non-JSON error body without Retry-After — return as-is so the generator's
        // own ApiError path takes over. (Authlimiter returns
        // text/plain; that's the canonical case for "no Problem".)
      }
      return resp;
    };

    // Classify raw HTTP statuses before decoding the final Problem body.
    const retryLayer: typeof globalThis.fetch = async (input, init) => {
      const signal = init?.signal;
      // Fetch consumes Request and ReadableStream bodies. Do not replay a
      // one-shot upload; generated JSON/FormData bodies are reusable.
      const body = init?.body ?? (input instanceof Request ? input.body : undefined);
      const attempts = body instanceof ReadableStream ? 1 : Math.max(1, retry.maxAttempts);
      for (let attempt = 0; attempt < attempts; attempt++) {
        signal?.throwIfAborted();
        // Transport errors propagate unchanged; only HTTP 429/5xx retry.
        const response = await loggerLayer(input, init);
        if ((response.status < 500 && response.status !== 429) || attempt + 1 === attempts) return response;
        const delay = Math.max(retry.backoffMs * 2 ** attempt, retryAfterMs(response.headers.get('Retry-After')));
        // Release intermediate bodies only. The final body must remain intact
        // for typed errors and the generated client's normal response parser.
        await response.body?.cancel().catch(() => undefined);
        await waitForRetry(delay, signal);
      }
      throw new Error('retry: budget exhausted without response');
    };
    return idempotencyLayer;
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
