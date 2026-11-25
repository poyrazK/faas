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

const subscriptionLimit = 64;
const readConcurrency = 4;
const initialPollDelayMs = 2_000;

export type McpTaskWatchEvent =
  | { readonly taskId: string; readonly outcome: 'available'; readonly task: McpTaskInboxEntry }
  | { readonly taskId: string; readonly outcome: 'unavailable'; readonly error: McpTaskReadError };

export interface McpTaskWatchOptions {
  readonly signal?: AbortSignal;
  /** Total deadline in milliseconds, default 120000. */
  readonly timeoutMs?: number;
  /** Interval for reconciling a quiet stream, default 30000 milliseconds. */
  readonly reconcileIntervalMs?: number;
}

async function openWatch(
  connection: McpTaskWatchConnection, taskIds: readonly string[], signal: AbortSignal, requestTimeout: number,
) {
  if (!connection.listen) return undefined;
  const lifetime = new AbortController();
  const active = AbortSignal.any([signal, lifetime.signal]);
  let wake: (() => void) | undefined;
  const pending = new Set<string>();
  let ended = false;
  const registration = registerTaskWatch(connection.transport, taskIds, id => {
    pending.add(id);
    wake?.();
  });
  if (!registration) return undefined;

  let subscription: Awaited<ReturnType<NonNullable<McpTaskWatchConnection['listen']>>> | undefined;
  const close = () => {
    registration.close();
    lifetime.abort();
    void subscription?.close().catch(() => undefined);
  };
  try {
    subscription = await connection.listen(registration.filter, { signal: active, timeout: requestTimeout });
    if (!registration.honored?.size) {
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
    wake?.();
  });
  return {
    get ended() { return ended; },
    honored: registration.honored,
    close,
    takeNotifications() {
      const ids = [...pending];
      pending.clear();
      return ids;
    },
    async wait(intervalMs: number) {
      if (!ended && pending.size === 0) {
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
    },
  };
}

function milliseconds(value: number, label: string, min: number): number {
  if (!Number.isInteger(value) || value < min || value > 2 ** 31 - 1) throw new RangeError(`Invalid MCP Task watch ${label}`);
  return value;
}

type TaskObservation = {
  taskId: string;
  dueAt: number;
  pollMs: number;
  retryMs: number;
  previous?: string;
  pollOnly?: boolean;
};

type ActiveSubscription = Awaited<ReturnType<typeof openWatch>>;

function watchTasks<Value>(
  connection: McpTaskWatchConnection, taskIds: readonly string[], options: McpTaskWatchOptions,
  project: (event: McpTaskWatchEvent) => Value,
): AsyncGenerator<Value, void, unknown> {
  if (!taskIds.every(isMcpTaskId)) throw new TypeError('Invalid MCP Task ID');
  const active = new Map<string, TaskObservation>(taskIds.map(taskId => [taskId, {
    taskId, dueAt: 0, pollMs: initialPollDelayMs, retryMs: initialPollDelayMs,
  }]));
  const timeout = milliseconds(options.timeoutMs ?? 120_000, 'timeout', 1);
  const reconcile = milliseconds(options.reconcileIntervalMs ?? 30_000, 'reconciliation interval', 250);
  const stop = new AbortController();

  async function* snapshots(): AsyncGenerator<Value, void, unknown> {
    const deadline = AbortSignal.timeout(timeout);
    const signal = AbortSignal.any([stop.signal, deadline, ...(options.signal ? [options.signal] : [])]);
    const requestTimeout = Math.min(timeout, 30_000);
    let watch: ActiveSubscription;
    let subscribe = true;
    let disconnects = 0;
    let reconnectAt = 0;

    async function read(state: TaskObservation): Promise<McpTaskWatchEvent | undefined> {
      let task: McpTaskInboxEntry;
      try {
        task = await readMcpTaskMetadata(connection, state.taskId, { signal, timeoutMs: requestTimeout });
      } catch (error) {
        signal.throwIfAborted();
        if (isRetryableMcpTaskRead(error)) {
          state.dueAt = performance.now() + state.retryMs;
          state.retryMs = Math.max(state.pollMs, Math.min(2 * state.retryMs, 60_000));
          return undefined;
        }
        if (!(error instanceof McpTaskReadError)) throw error;
        active.delete(state.taskId);
        return { taskId: state.taskId, outcome: 'unavailable', error };
      }

      state.pollMs = mcpTaskPollDelay(task);
      state.retryMs = state.pollMs;
      if (task.status !== 'working') active.delete(state.taskId);
      state.dueAt = performance.now() + (watch?.honored.has(state.taskId) ? Math.max(reconcile, state.pollMs) : state.pollMs);
      const fingerprint = JSON.stringify(task);
      if (fingerprint === state.previous) return undefined;
      state.previous = fingerprint;
      return { taskId: state.taskId, outcome: 'available', task };
    }

    try {
      while (active.size > 0) {
        signal.throwIfAborted();
        // Read every remaining Task before opening another stream.
        if (watch?.ended) {
          watch.close();
          watch = undefined;
          disconnects++;
          subscribe = disconnects < 3;
          reconnectAt = performance.now() + 250 * 2 ** (disconnects - 1);
          for (const state of active.values()) state.dueAt = 0;
        }
        const now = performance.now();
        for (const id of watch?.takeNotifications() ?? []) {
          const state = active.get(id);
          if (state) state.dueAt = Math.min(state.dueAt, now);
        }
        const due: TaskObservation[] = [];
        let nextRead = Infinity;
        for (const state of active.values()) {
          if (state.dueAt <= now) due.push(state);
          nextRead = Math.min(nextRead, state.dueAt);
        }
        // Once the initial or recovery reads have run, ordinary poll deadlines
        // must not keep a busy batch from opening its subscription.
        const eligible = !watch && subscribe && nextRead !== 0 && active.size <= subscriptionLimit
          ? [...active.values()].filter(state => state.previous !== undefined && !state.pollOnly) : [];
        if (eligible.length > 0 && now >= reconnectAt) {
          watch = await openWatch(connection, eligible.map(state => state.taskId), signal, requestTimeout);
          subscribe = watch !== undefined;
          if (watch) {
            for (const state of eligible) {
              if (watch.honored.has(state.taskId)) state.dueAt = 0;
              else state.pollOnly = true;
            }
          }
          continue;
        }
        if (due.length > 0) {
          due.sort((a, b) => a.dueAt - b.dueAt);
          const events = await Promise.all(due.slice(0, readConcurrency).map(read));
          if (watch && !watch.ended && ![...watch.honored].some(id => active.has(id))) {
            watch.close();
            watch = undefined;
          }
          for (const event of events) if (event) yield project(event);
          continue;
        }
        const wakeAt = eligible.length > 0 ? Math.min(nextRead, reconnectAt) : nextRead;
        const millisecondsUntilWake = Math.max(1, Math.ceil(wakeAt - now));
        if (watch) await watch.wait(millisecondsUntilWake);
        else await delay(millisecondsUntilWake, undefined, { signal });
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

/** Yield authorized status snapshots until the Task finishes or needs input. */
export function watchMcpTask(
  connection: McpTaskWatchConnection, taskId: string, options: McpTaskWatchOptions = {},
): AsyncGenerator<McpTaskInboxEntry, void, unknown> {
  return watchTasks(connection, [taskId], options, event => {
    if (event.outcome === 'unavailable') throw event.error;
    return event.task;
  });
}

/** Watch known Tasks on a shared subscription with per-Task snapshots and failures. */
export function watchMcpTasks(
  connection: McpTaskWatchConnection, taskIds: readonly string[], options: McpTaskWatchOptions = {},
): AsyncGenerator<McpTaskWatchEvent, void, unknown> {
  if (!Array.isArray(taskIds)) throw new TypeError('Invalid MCP Task IDs');
  return watchTasks(connection, [...new Set(taskIds)], options, event => event);
}
