import type { RetryPolicy } from './http-transport.js';

/** Metadata for one HTTP response, including intermediate retry attempts.
 * Duration measures monotonic client time until response headers arrive.
 * Bodies, URLs, credentials and arbitrary headers are never included.
 */
export interface ResponseInfo {
  readonly requestId: string | null;
  readonly status: number;
  readonly attempt: number;
  readonly durationMs: number;
}

/** Exceptions and rejected promises from observers do not affect requests.
 * Async completion is not awaited. */
export type ResponseObserver = (info: ResponseInfo) => void | Promise<void>;

/** Optional second argument on isolated generated service methods. */
export interface RequestOptions {
  readonly signal?: AbortSignal;
  /** Total budget including headers, retries, backoff and body consumption.
   * Zero disables the deadline. Accepts integers from 0 through 2^31-1. */
  readonly timeoutMs?: number;
  /** Override the client policy for this call; false disables HTTP retries. */
  readonly retry?: RetryPolicy | false;
  /** Runs alongside the client observer, once per HTTP response. */
  readonly onResponse?: ResponseObserver;
}
