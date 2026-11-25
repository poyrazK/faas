import { createHash } from 'node:crypto';
import {
  acknowledgeTaskGroupUpdate, loadTaskGroup, taskGroupReceiptPath, taskGroupUpdateAcknowledged,
} from './mcp-task-group-file.js';
import type { McpTaskInboxEntry } from './mcp-task-inbox.js';
import {
  loadPlannedMcpTaskReceipt, mcpTaskReceiptEndpoint, McpTaskReceiptError, type McpTaskReceiptConnection,
} from './mcp-task-receipt.js';
import { McpTaskReadError } from './mcp-task-result.js';
import {
  lookupMcpTaskBySubmissionKey, McpTaskSubmissionError, type McpTaskSubmissionConnection,
} from './mcp-task-submission.js';
import { watchTaskEvents, type McpTaskWatchConnection, type McpTaskWatchOptions } from './mcp-task-watch.js';

export { createMcpTaskGroup, McpTaskGroupError, type McpTaskGroupFailureCode } from './mcp-task-group-file.js';

export type McpTaskGroupConnection = McpTaskReceiptConnection & McpTaskWatchConnection;

export interface McpTaskGroupWatchOptions extends McpTaskWatchOptions {
  /** Keep observing Tasks while they are waiting for input. Defaults to false. */
  readonly followInput?: boolean;
}

type UnavailableTask = {
  readonly receiptIndex: number;
  readonly outcome: 'unavailable';
  readonly error: McpTaskReadError | McpTaskSubmissionError;
};

export type McpTaskGroupEvent = UnavailableTask | {
  readonly receiptIndex: number;
  readonly outcome: 'available';
  readonly task: McpTaskInboxEntry;
  readonly updateId: string;
  /** Persist this exact update's acknowledgement after handling it. */
  acknowledge(): Promise<void>;
};

export function groupConnections(connection: McpTaskGroupConnection, endpoint: string) {
  const transport = connection.transport;
  if (mcpTaskReceiptEndpoint(connection) !== endpoint) throw new McpTaskReceiptError('endpoint_mismatch');
  const check = () => {
    if (connection.transport !== transport) throw new McpTaskReceiptError('endpoint_mismatch');
  };
  const capabilities = {
    getNegotiatedProtocolVersion() { check(); return connection.getNegotiatedProtocolVersion(); },
    getServerCapabilities() { check(); return connection.getServerCapabilities(); },
  };
  const lookup: McpTaskSubmissionConnection = {
    ...capabilities,
    request(...args) { check(); return connection.request(...args); },
  };
  const tasks: McpTaskWatchConnection = {
    ...capabilities,
    get transport() { check(); return transport; },
    request(...args) { check(); return connection.request(...args); },
  };
  if (connection.listen) {
    const listen = connection.listen.bind(connection);
    tasks.listen = (...args) => { check(); return listen(...args); };
  }
  return { lookup, tasks, check };
}

function updateId(group: string, index: number, task: McpTaskInboxEntry): string {
  return createHash('sha256').update(JSON.stringify([
    group, index, task.taskId, task.createdAt, task.lastUpdatedAt, task.status,
  ])).digest('hex');
}

/** Resume an existing group with authenticated status reads and a shared live watch. */
export function watchMcpTaskGroup(
  connection: McpTaskGroupConnection, path: string, { followInput = false, ...options }: McpTaskGroupWatchOptions = {},
): AsyncGenerator<McpTaskGroupEvent, void, unknown> {
  const timeout = options.timeoutMs ?? 120_000;
  if (!Number.isInteger(timeout) || timeout < 1 || timeout > 2 ** 31 - 1) {
    throw new RangeError('Invalid MCP Task group timeout');
  }
  const stop = new AbortController();

  async function* updates(): AsyncGenerator<McpTaskGroupEvent, void, unknown> {
    const deadline = AbortSignal.timeout(timeout);
    const signal = AbortSignal.any([stop.signal, deadline, ...(options.signal ? [options.signal] : [])]);
    try {
      signal.throwIfAborted();
      const { path: directory, manifest } = await loadTaskGroup(path);
      const clients = groupConnections(connection, manifest.endpointSha256);
      const entries = new Map<string, number[]>();
      const seen = new Map<number, string>();
      const lookupOptions = { signal, timeoutMs: Math.min(30_000, timeout) };

      // Resolve receipts with lost replies independently and with bounded concurrency.
      for (let start = 0; start < manifest.members.length; start += 4) {
        const references = await Promise.all(manifest.members.slice(start, start + 4).map(async (member, offset) => {
          const receiptIndex = start + offset;
          try {
            const planned = manifest.version === 2 ? manifest.members[receiptIndex] : undefined;
            const receipt = planned ? await loadPlannedMcpTaskReceipt(taskGroupReceiptPath(directory, receiptIndex),
              { endpointSha256: manifest.endpointSha256, ...planned }) : member;
            if (!receipt) return { receiptIndex };
            clients.check();
            const taskId = receipt.taskId ?? (await lookupMcpTaskBySubmissionKey(clients.lookup, receipt.submissionKey, lookupOptions)).taskId;
            return { taskId, receiptIndex };
          } catch (error) {
            signal.throwIfAborted();
            clients.check();
            if (error instanceof McpTaskSubmissionError) return { error, receiptIndex };
            if (error instanceof DOMException && error.name === 'TimeoutError') {
              return { error: new McpTaskReadError('timeout'), receiptIndex };
            }
            throw error;
          }
        }));
        signal.throwIfAborted();
        clients.check();
        for (const reference of references) {
          const { receiptIndex } = reference;
          if (reference.error) yield { receiptIndex, outcome: 'unavailable', error: reference.error };
          else if (reference.taskId !== undefined) {
            const indexes = entries.get(reference.taskId);
            if (indexes) indexes.push(receiptIndex);
            else entries.set(reference.taskId, [receiptIndex]);
          }
        }
      }

      const events = watchTaskEvents(clients.tasks, [...entries.keys()], { ...options, signal, timeoutMs: timeout }, followInput);
      for await (const event of events) {
        clients.check();
        for (const receiptIndex of entries.get(event.taskId)!) {
          signal.throwIfAborted();
          if (event.outcome === 'unavailable') {
            yield { receiptIndex, outcome: 'unavailable', error: event.error };
            continue;
          }
          const id = updateId(manifest.id, receiptIndex, event.task);
          if (seen.get(receiptIndex) === id) continue;
          seen.set(receiptIndex, id);
          if (await taskGroupUpdateAcknowledged(directory, id)) continue;
          signal.throwIfAborted();
          clients.check();
          yield {
            receiptIndex, outcome: 'available', task: event.task, updateId: id,
            acknowledge: () => acknowledgeTaskGroupUpdate(directory, manifest.id, id),
          };
        }
      }
    } catch (error) {
      options.signal?.throwIfAborted();
      if (deadline.aborted) throw new McpTaskReadError('timeout');
      throw error;
    }
  }

  const iterator = updates();
  return {
    next: (...args) => iterator.next(...args),
    return(value) { stop.abort(); return iterator.return(value); },
    throw(reason) { stop.abort(reason); return iterator.throw(reason); },
    [Symbol.asyncIterator]() { return this; },
  };
}
