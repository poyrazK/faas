// src/executions.ts — typed, resumable disposable execution events.
//
// The generated RunsService method intentionally remains a raw string
// response for backwards compatibility. This façade consumes the SSE body
// incrementally, decodes the wire payloads, and reconnects after an
// established stream ends before its terminal event.

import { OpenAPI } from './generated/index.js';
import { streamSse, type SseEvent } from './sse.js';

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

function executionEventsURL(
  executionID: string,
  after: number,
  limit: number,
): URL {
  const url = new URL(
    `/v1/executions/${encodeURIComponent(executionID)}/events`,
    OpenAPI.BASE,
  );
  if (after > 0) url.searchParams.set('after', String(after));
  url.searchParams.set('limit', String(limit));
  return url;
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

/**
 * Watch a disposable execution until its terminal event.
 *
 * The first connection uses `after` (if supplied). Once a stream has been
 * established, an EOF or transient read failure reconnects with the latest
 * event id, so callers do not need to implement cursor bookkeeping.
 */
export async function* watchExecution(
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
      response = await globalThis.fetch(executionEventsURL(executionID, after, limit), {
        method: 'GET',
        headers: executionHeaders(),
        signal: options.signal,
      });
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
        if (event.id !== undefined && event.id > after) after = event.id;
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
