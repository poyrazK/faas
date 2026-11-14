// src/executions.ts — typed, resumable disposable execution events.
//
// The generated RunsService method intentionally remains a raw string
// response for backwards compatibility. This façade consumes the SSE body
// incrementally, decodes the wire payloads, and reconnects after an
// established stream ends before its terminal event.

import { OpenAPI } from './generated/index.js';
import { RunsService } from './generated/services/RunsService.js';
import type { CreateExecutionRequest } from './generated/models/CreateExecutionRequest.js';
import type { ExecutionResponse } from './generated/models/ExecutionResponse.js';
import { streamSse, type SseEvent } from './sse.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';

/** Event names emitted by `/v1/executions/{id}/events`. */
export type ExecutionEventType =
  | 'status'
  | 'stdout'
  | 'stderr'
  | 'terminal'
  | 'error'
  | (string & {});

/** JSON payload carried by an execution event.
 *
 * The index signature keeps the client forward-compatible with new event
 * fields while documenting the fields currently emitted by apid.
 */
export interface ExecutionEventData {
  status?: string;
  chunk?: string;
  output_truncated?: boolean;
  exit_code?: number | null;
  failure_code?: string;
  failure_message?: string;
  usage?: Record<string, unknown> | null;
  [key: string]: unknown;
}

/** A decoded execution event with its resumable cursor. */
export interface ExecutionEvent {
  type: ExecutionEventType;
  id?: number;
  data: ExecutionEventData;
  /** Original parsed SSE frame, for callers that need wire details. */
  raw: SseEvent;
}

/** Options for `watchExecution`. */
export interface WatchExecutionOptions {
  /** Resume after this event id (exclusive). */
  after?: number;
  /** Maximum replay batch requested from apid. */
  limit?: number;
  /** Stop the stream and reject when aborted. */
  signal?: AbortSignal;
  /** Initial reconnect delay after a disconnected stream. */
  retryInitialMs?: number;
  /** Maximum reconnect delay. */
  retryMaxMs?: number;
}

/** Options for execution submission and observation. */
export interface RunExecutionOptions extends WatchExecutionOptions {
  /** Stable key for replaying the create request, when desired. */
  idempotencyKey?: string;
  /** Called in stream order for every status, output, and terminal event. */
  onEvent?: (event: ExecutionEvent) => void | Promise<void>;
}

/** Convenience operations on the isolated preview client. */
export interface ExecutionHelpers {
  watchExecution(id: string, options?: WatchExecutionOptions): AsyncGenerator<ExecutionEvent, void, void>;
  runExecution(requestBody: CreateExecutionRequest, options?: RunExecutionOptions): Promise<ExecutionResponse>;
}

/** Internal request boundary shared by isolated and legacy helpers. */
export interface ExecutionRequests {
  runs: Pick<typeof RunsService, 'createExecution' | 'getExecution'>;
  fetchEvents(path: string, signal?: AbortSignal): Promise<Response>;
}

class ExecutionEventParseError extends Error {}

function parseEventData(raw: SseEvent): ExecutionEventData {
  if (raw.data === '') return {};
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw.data);
  } catch (err) {
    throw new ExecutionEventParseError(
      `execution event ${raw.event} contains invalid JSON: ${String(err)}`,
    );
  }
  if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
    return parsed as ExecutionEventData;
  }
  return { value: parsed };
}

function decodeEvent(raw: SseEvent): ExecutionEvent {
  const id = raw.id === undefined ? undefined : Number(raw.id);
  return {
    type: raw.event,
    ...(id !== undefined && Number.isSafeInteger(id) ? { id } : {}),
    data: parseEventData(raw),
    raw,
  };
}

function isRetryableError(err: unknown): boolean {
  if (err instanceof ExecutionEventParseError || err instanceof SyntaxError) return false;
  const status = err && typeof err === 'object' && 'status' in err
    ? (err as { status?: unknown }).status
    : undefined;
  if (typeof status === 'number') return status === 408 || status === 429 || status >= 500;
  return true;
}

function abortReason(signal?: AbortSignal): unknown {
  return signal?.reason ?? new Error('execution event stream aborted');
}

async function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  if (ms <= 0) return;
  await new Promise<void>((resolve, reject) => {
    let timer: ReturnType<typeof setTimeout>;
    const cleanup = () => {
      if (signal) signal.removeEventListener('abort', onAbort);
    };
    const onAbort = () => {
      clearTimeout(timer);
      cleanup();
      reject(abortReason(signal));
    };
    timer = setTimeout(() => {
      cleanup();
      resolve();
    }, ms);
    if (signal) {
      if (signal.aborted) {
        clearTimeout(timer);
        cleanup();
        reject(abortReason(signal));
        return;
      }
      signal.addEventListener('abort', onAbort, { once: true });
    }
  });
}

function executionEventsPath(executionID: string, after: number, limit: number): string {
  const query = new URLSearchParams();
  if (after > 0) query.set('after', String(after));
  query.set('limit', String(limit));
  return `/v1/executions/${encodeURIComponent(executionID)}/events?${query}`;
}

function executionHeaders(): Headers {
  const headers = new Headers();
  if (OpenAPI.HEADERS && typeof OpenAPI.HEADERS !== 'function') {
    for (const [key, value] of Object.entries(OpenAPI.HEADERS)) headers.set(key, value);
  }
  if (OpenAPI.TOKEN && typeof OpenAPI.TOKEN === 'string' && !headers.has('Authorization')) {
    headers.set('Authorization', `Bearer ${OpenAPI.TOKEN}`);
  }
  headers.set('Accept', 'text/event-stream');
  return headers;
}

// Compatibility exports continue to resolve the process-wide configuration.
const legacyRequests: ExecutionRequests = {
  runs: RunsService,
  fetchEvents(path, signal) {
    return globalThis.fetch(new URL(path, OpenAPI.BASE), {
      method: 'GET', headers: executionHeaders(), signal,
    });
  },
};

/** Watch using the legacy process-wide configuration. For independent callers,
 * use createClient from the preview entry and client.watchExecution(...).
 */
export function watchExecution(
  executionID: string,
  options: WatchExecutionOptions = {},
): AsyncGenerator<ExecutionEvent, void, void> {
  return watchExecutionWithClient(legacyRequests, executionID, options);
}

/**
 * Watch a disposable execution until its terminal event.
 *
 * The first connection uses `after` (if supplied). Once a stream has been
 * established, an EOF or transient read failure reconnects with the latest
 * event id, so callers do not need to implement cursor bookkeeping.
 */
export async function* watchExecutionWithClient(
  client: ExecutionRequests,
  executionID: string,
  options: WatchExecutionOptions = {},
): AsyncGenerator<ExecutionEvent, void, void> {
  let after = Math.max(0, options.after ?? 0);
  const limit = Math.max(1, options.limit ?? 100);
  const initialDelay = Math.max(0, options.retryInitialMs ?? 100);
  const maxDelay = Math.max(initialDelay, options.retryMaxMs ?? 2_000);
  let delay = initialDelay;
  let connected = false;

  while (true) {
    if (options.signal?.aborted) throw abortReason(options.signal);

    let response: Response;
    try {
      response = await client.fetchEvents(executionEventsPath(executionID, after, limit), options.signal);
    } catch (err) {
      if (options.signal?.aborted) throw abortReason(options.signal);
      if (!connected || !isRetryableError(err)) throw err;
      await sleep(delay, options.signal);
      delay = Math.min(Math.max(delay * 2, initialDelay), maxDelay);
      continue;
    }

    if (!response.ok) {
      const err = new Error(`execution event stream returned HTTP ${response.status}`) as Error & {
        status?: number;
      };
      err.status = response.status;
      try { await response.body?.cancel(); } catch { /* best effort */ }
      if (!connected || !isRetryableError(err)) throw err;
      await sleep(delay, options.signal);
      delay = Math.min(Math.max(delay * 2, initialDelay), maxDelay);
      continue;
    }

    if (!response.body) {
      throw new Error('execution event stream response has no body');
    }

    connected = true;
    let terminal = false;
    try {
      for await (const raw of streamSse(response, options.signal)) {
        if (options.signal?.aborted) throw abortReason(options.signal);
        const event = decodeEvent(raw);
        // A reconnect may replay an overlapping batch. Deliver each identified
        // event once; cursorless events keep their original delivery semantics.
        if (event.id !== undefined) {
          if (event.id <= after) continue;
          after = event.id;
        }
        if (event.type === 'terminal') terminal = true;
        yield event;
        if (terminal) return;
      }
    } catch (err) {
      if (options.signal?.aborted) throw abortReason(options.signal);
      if (!isRetryableError(err)) throw err;
    } finally {
      try { await response.body?.cancel(); } catch { /* best effort */ }
    }

    if (terminal) return;
    await sleep(delay, options.signal);
    delay = Math.min(Math.max(delay * 2, initialDelay), maxDelay);
  }
}

/**
 * Submit one disposable execution, consume its resumable event stream, and
 * return the final receipt. The create response is intentionally not treated
 * as completion: agents receive live output and the final GET is made only
 * after the terminal event, when the VM has been destroyed.
 */
export function runExecution(
  requestBody: CreateExecutionRequest,
  options: RunExecutionOptions = {},
): Promise<ExecutionResponse> {
  return runExecutionWithClient(legacyRequests, requestBody, options);
}

/** Internal execution workflow; its request boundary stays fixed throughout. */
export async function runExecutionWithClient(
  client: ExecutionRequests,
  requestBody: CreateExecutionRequest,
  options: RunExecutionOptions = {},
): Promise<ExecutionResponse> {
  const { idempotencyKey, onEvent, ...watchOptions } = options;
  if (watchOptions.signal?.aborted) throw abortReason(watchOptions.signal);

  const receipt = await withSignal(
    client.runs.createExecution({ requestBody, idempotencyKey }), watchOptions.signal,
  );

  for await (const event of watchExecutionWithClient(client, receipt.id, watchOptions)) {
    if (onEvent) await onEvent(event);
    if (event.type === 'terminal') break;
  }
  if (watchOptions.signal?.aborted) throw abortReason(watchOptions.signal);
  return withSignal(client.runs.getExecution({ id: receipt.id }), watchOptions.signal);
}

/** Cancel only this generated request and preserve the caller's abort reason. */
async function withSignal<T>(request: CancelablePromise<T>, signal?: AbortSignal): Promise<T> {
  const onAbort = () => request.cancel();
  if (signal?.aborted) {
    request.cancel();
    // Observe the cancelled promise before propagating the caller's reason.
    await request.catch(() => undefined);
    throw abortReason(signal);
  }
  signal?.addEventListener('abort', onAbort, { once: true });
  try {
    return await request;
  } catch (error) {
    if (signal?.aborted) throw abortReason(signal);
    throw error;
  } finally {
    signal?.removeEventListener('abort', onAbort);
  }
}
