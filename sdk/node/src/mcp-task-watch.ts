import { setTimeout as delay } from 'node:timers/promises';
import { isMcpTaskId, type McpTaskInboxEntry } from './mcp-task-inbox.js';
import {
  isRetryableMcpTaskRead, McpTaskReadError, mcpTaskPollDelay, readMcpTaskMetadata, type McpTaskConnection,
} from './mcp-task-result.js';
import { registerTaskWatch, type TaskListenFilter } from './mcp-task-watch-transport.js';

export interface McpTaskWatchConnection extends McpTaskConnection {
  readonly transport?: object | undefined;
  listen?(filter: TaskListenFilter, options: { signal: AbortSignal; timeout: number }): Promise<{
    readonly closed: Promise<'local' | 'graceful' | 'remote'>;
    close(): Promise<void>;
  }>;
}

export interface McpTaskWatchOptions {
  readonly signal?: AbortSignal;
  /** Total deadline in milliseconds, default 120000. */
  readonly timeoutMs?: number;
  /** Interval for reconciling a quiet stream, default 30000 milliseconds. */
  readonly reconcileIntervalMs?: number;
}

async function openWatch(
  connection: McpTaskWatchConnection, taskId: string, signal: AbortSignal, requestTimeout: number,
) {
  if (!connection.listen) return undefined;
  const lifetime = new AbortController();
  const active = AbortSignal.any([signal, lifetime.signal]);
  let wake: (() => void) | undefined;
  let pending = false;
  let ended = false;
  const notify = () => { pending = true; wake?.(); };
  const registration = registerTaskWatch(connection.transport, taskId, notify);
  if (!registration) return undefined;

  let subscription: Awaited<ReturnType<NonNullable<McpTaskWatchConnection['listen']>>> | undefined;
  const close = () => {
    registration.close();
    lifetime.abort();
    void subscription?.close().catch(() => undefined);
  };
  try {
    subscription = await connection.listen(registration.filter, { signal: active, timeout: requestTimeout });
    if (!registration.acknowledged) {
      close();
      return undefined;
    }
  } catch {
    close();
    signal.throwIfAborted();
    return undefined;
  }

  void subscription.closed.then(() => {
    registration.close();
    ended = true;
    notify();
  });
  return {
    get ended() { return ended; },
    close,
    async wait(intervalMs: number) {
      if (!pending) {
        const activity = new AbortController();
        wake = () => activity.abort();
        try {
          await delay(intervalMs, undefined, { signal: AbortSignal.any([active, activity.signal]) });
        } catch (error) {
          signal.throwIfAborted();
          if (!activity.signal.aborted) throw error;
        } finally {
          wake = undefined;
        }
      }
      pending = false;
    },
  };
}

function milliseconds(value: number, label: string, min: number): number {
  if (!Number.isInteger(value) || value < min || value > 2 ** 31 - 1) throw new RangeError(`Invalid MCP Task watch ${label}`);
  return value;
}

/** Yield authorized status snapshots until the Task finishes or needs input. */
export function watchMcpTask(
  connection: McpTaskWatchConnection, taskId: string, options: McpTaskWatchOptions = {},
): AsyncGenerator<McpTaskInboxEntry, void, unknown> {
  if (!isMcpTaskId(taskId)) throw new TypeError('Invalid MCP Task ID');
  const timeout = milliseconds(options.timeoutMs ?? 120_000, 'timeout', 1);
  const reconcile = milliseconds(options.reconcileIntervalMs ?? 30_000, 'reconciliation interval', 250);
  const stop = new AbortController();

  async function* snapshots(): AsyncGenerator<McpTaskInboxEntry, void, unknown> {
    const deadline = AbortSignal.timeout(timeout);
    const signal = AbortSignal.any([stop.signal, deadline, ...(options.signal ? [options.signal] : [])]);
    const requestTimeout = Math.min(timeout, 30_000);
    let watch: Awaited<ReturnType<typeof openWatch>>;
    let subscribe = true;
    let disconnects = 0;
    let previous = '';
    let pollMs = 2_000;
    let retryMs = pollMs;
    try {
      for (;;) {
        let task: McpTaskInboxEntry;
        try {
          task = await readMcpTaskMetadata(connection, taskId, { signal, timeoutMs: requestTimeout });
        } catch (error) {
          signal.throwIfAborted();
          if (!isRetryableMcpTaskRead(error)) throw error;
          await delay(retryMs, undefined, { signal });
          retryMs = Math.max(pollMs, Math.min(2 * retryMs, 60_000));
          continue;
        }

        pollMs = mcpTaskPollDelay(task);
        retryMs = pollMs;
        const fingerprint = JSON.stringify(task);
        const changed = fingerprint !== previous;
        previous = fingerprint;
        if (task.status !== 'working') {
          watch?.close();
          watch = undefined;
          if (changed) yield task;
          return;
        }
        if (changed) yield task;
        signal.throwIfAborted();

        // Reconcile the durable state before reconnecting to a lost stream.
        if (watch?.ended) {
          watch.close();
          watch = undefined;
          disconnects++;
          subscribe = disconnects < 3;
          if (subscribe) await delay(250 * 2 ** (disconnects - 1), undefined, { signal });
        }
        if (!watch && subscribe) {
          watch = await openWatch(connection, taskId, signal, requestTimeout);
          subscribe = watch !== undefined;
          if (watch) continue; // Cover changes between the initial read and the acknowledgement.
        }
        if (watch) await watch.wait(Math.max(reconcile, pollMs));
        else await delay(pollMs, undefined, { signal });
      }
    } catch (error) {
      options.signal?.throwIfAborted();
      if (deadline.aborted) throw new McpTaskReadError('timeout');
      throw error;
    } finally {
      watch?.close();
    }
  }

  const iterator = snapshots();
  return {
    next: (...args) => iterator.next(...args),
    return(value) { stop.abort(); return iterator.return(value); },
    throw(reason) { stop.abort(reason); return iterator.throw(reason); },
    [Symbol.asyncIterator]() { return this; },
  };
}
