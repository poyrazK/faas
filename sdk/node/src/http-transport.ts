// Shared request policy; creating a transport never changes process globals.
import timers from 'node:timers/promises';
import { isFaasError, problemToError, type FaasError, type Problem } from './errors.js';
import { isMutating, mintIdempotencyKey } from './idempotency.js';
import type { RequestOptions, ResponseInfo, ResponseObserver } from './request-options.js';

// Capture the platform transport before a legacy FaaSClient can install its wrapper.
const platformFetch = globalThis.fetch;

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

/** A traffic policy check that ran out of time on a busy control plane
 *  (422 traffic_policy_too_complex with Retry-After). The refusal saved
 *  nothing and asks to be resent. Reads a clone, so the body stays intact. */
async function policyCheckTimedOut(response: Response): Promise<boolean> {
  if (response.status !== 422 || !response.headers.get('Retry-After')) return false;
  try {
    const problem: unknown = await response.clone().json();
    return typeof problem === 'object' && problem !== null && (problem as { code?: unknown }).code === 'traffic_policy_too_complex';
  } catch {
    return false;
  }
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

/** Retry policy. `maxAttempts` is the TOTAL number of attempts
 *  (initial + retries), capped at nine; `maxAttempts:1` disables retry.
 *  A nonpositive backoff uses 100ms; exponential growth stops at 8s. */
export interface RetryPolicy {
  maxAttempts: number;
  backoffMs: number;
}

/** Options shared by `createClient(...)` and the legacy `FaaSClient`. */
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
  /** Request timeout in milliseconds, including retries. Defaults to 30s
   *  for ordinary requests; SSE uses its caller's signal. Zero disables it. */
  timeoutMs?: number;
  /** Optional override for the underlying fetch. Used by tests to
   *  inject a stub. Production callers leave this unset. */
  fetch?: typeof globalThis.fetch;
  /** Observe each HTTP response, including retry attempts. Callback failures
   * and asynchronous completion never change the request's outcome. */
  onResponse?: ResponseObserver;
}

function observe(observer: ResponseObserver | undefined, info: ResponseInfo): void {
  try { Promise.resolve(observer?.(info)).catch(() => undefined); } catch { /* observer only */ }
}

function responseInfo(response: Response, attempt: number, start: number): ResponseInfo {
  const id = response.headers.get('X-Request-ID');
  return Object.freeze({
    requestId: id && /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(id) ? id : null,
    status: response.status, attempt, durationMs: Math.max(0, performance.now() - start),
  });
}

export function createHttpTransport(
  options: FaaSClientOptions,
  defaultIdempotencyKey?: () => string | undefined,
  requestOptions?: RequestOptions,
): typeof globalThis.fetch {
  const userFetch = options.fetch ?? platformFetch;
  const retry = requestOptions?.retry === false ? { maxAttempts: 1, backoffMs: 0 }
    : { ...(requestOptions?.retry ?? options.retry ?? { maxAttempts: 1, backoffMs: 0 }) };
  const logger = options.logger ?? noopLogger;
  const timeoutMs = requestOptions?.timeoutMs ?? options.timeoutMs;
  // Prepare request identity once, outside the per-attempt transport.
  const idempotencyLayer: typeof globalThis.fetch = async (input, init) => {
    const reqInit = init ? { ...init } : {};
    const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
    const method = (reqInit.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();
    reqInit.method = method;
    if (reqInit.signal === undefined && input instanceof Request) reqInit.signal = input.signal;
    if (isMutating(method) && !headers.has('Idempotency-Key')) {
      const defaultKey = defaultIdempotencyKey?.();
      headers.set('Idempotency-Key', defaultKey ?? mintIdempotencyKey());
    }
    reqInit.headers = headers;
    const budgetMs = timeoutMs ?? (headers.get('Accept')?.includes('text/event-stream') ? 0 : 30_000);
    if (budgetMs > 0) {
      const timeout = AbortSignal.timeout(budgetMs);
      reqInit.signal = reqInit.signal ? AbortSignal.any([reqInit.signal, timeout]) : timeout;
    }
    return rfc7807Layer(input, reqInit);
  };

  // logger layer — one log line per attempt.
  const loggerLayer = async (input: Parameters<typeof fetch>[0], init: RequestInit | undefined, attempt: number): Promise<Response> => {
    const start = Date.now();
    const monotonicStart = performance.now();
    const url = typeof input === 'string'
      ? input
      : input instanceof URL ? input.toString() : input.url;
    logger.debug('faas http request', { method: init?.method ?? 'GET', url });
    try {
      const resp = await userFetch(input, init);
      if (requestOptions?.signal?.aborted) {
        // A custom fetch can resolve after cancellation. Retire its body and
        // suppress late observer delivery for the completed call.
        void resp.body?.cancel().catch(() => undefined);
        throw requestOptions.signal.reason;
      }
      const info = options.onResponse || requestOptions?.onResponse ? responseInfo(resp, attempt, monotonicStart) : undefined;
      const elapsed = Date.now() - start;
      logger.debug('faas http response', {
        method: init?.method ?? 'GET',
        url,
        status: resp.status,
        elapsed_ms: elapsed,
      });
      if (info) {
        observe(options.onResponse, info);
        observe(requestOptions?.onResponse, info);
      }
      // Only scoped generated calls wrap bodies. A custom fetch may ignore its
      // signal; piping with the scope signal still cancels a blocked body read.
      // Legacy global fetch retains the original Response object and metadata.
      if (requestOptions?.signal && resp.body) {
        return new Response(resp.body.pipeThrough(new TransformStream(), { signal: init?.signal ?? requestOptions.signal }), {
          status: resp.status, statusText: resp.statusText, headers: resp.headers,
        });
      }
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
    const attempts = body instanceof ReadableStream ? 1 : Math.min(9, Math.max(1, retry.maxAttempts));
    for (let attempt = 0; attempt < attempts; attempt++) {
      signal?.throwIfAborted();
      // Transport errors propagate unchanged; only HTTP 429/5xx and a
      // timed-out traffic policy check retry.
      const response = await loggerLayer(input, init, attempt + 1);
      const retryable = response.status >= 500 || response.status === 429 || await policyCheckTimedOut(response);
      if (!retryable || attempt + 1 === attempts) return response;
      const base = retry.backoffMs > 0 ? retry.backoffMs : 100;
      const delay = Math.max(Math.min(8_000, base * 2 ** attempt), retryAfterMs(response.headers.get('Retry-After')));
      // Release intermediate bodies only. The final body must remain intact
      // for typed errors and the generated client's normal response parser.
      await response.body?.cancel().catch(() => undefined);
      await waitForRetry(delay, signal);
    }
    throw new Error('retry: budget exhausted without response');
  };
  return idempotencyLayer;
}
