import { setTimeout as delay } from 'node:timers/promises';
import { parseMcpTaskFields, type McpTaskInboxEntry, type McpTaskInboxStatus } from './mcp-task-inbox.js';

const method = 'tasks/get';
const protocol = '2026-07-28';
const extension = 'io.modelcontextprotocol/tasks';
const readTimeoutMs = 30_000;
const initialPollMs = 2_000;
const minPollMs = 250;

// Validate the Task ID and state after the MCP client decodes the response.
const taskResultSchema = {
  '~standard': { version: 1 as const, vendor: 'gregale', validate: (value: unknown) => ({ value }) },
};
type RequestOptions = { signal: AbortSignal; timeout: number; allowInputRequired: true };

/** The official MCP Client supplies this interface after connecting. */
export interface McpTaskConnection {
  getNegotiatedProtocolVersion(): string | undefined;
  getServerCapabilities(): unknown;
  request(
    request: { method: typeof method; params: { taskId: string } },
    resultSchema: typeof taskResultSchema,
    options: RequestOptions,
  ): Promise<unknown>;
}

type TaskState<Status extends McpTaskInboxStatus> = McpTaskInboxEntry & { readonly status: Status };
type TaskPayload = Readonly<Record<string, unknown>>;

/** A terminal Task or one that needs input before it can proceed. */
export type McpTaskWaitResult =
  | TaskState<'input_required' | 'cancelled'>
  | (TaskState<'completed'> & { readonly result: TaskPayload })
  | (TaskState<'failed'> & { readonly error: TaskPayload });

export type McpTaskSnapshot = TaskState<'working'> | McpTaskWaitResult;

export interface McpTaskReadOptions {
  readonly signal?: AbortSignal;
  /** Total deadline in milliseconds. Defaults to 30000 for get and 120000 for wait. */
  readonly timeoutMs?: number;
}

const messages = {
  unavailable: 'MCP connection does not advertise Tasks on protocol 2026-07-28',
  invalid_task: 'Invalid MCP Task response',
  not_found: 'MCP Task is unavailable to this caller',
  timeout: 'MCP Task read deadline exceeded',
  request_failed: 'MCP Task read request failed',
} as const;

export type McpTaskReadFailureCode = keyof typeof messages;

/** Read failures expose a bounded code and optional HTTP status without server text. */
export class McpTaskReadError extends Error {
  override readonly name = 'McpTaskReadError';
  constructor(readonly code: McpTaskReadFailureCode, readonly status?: number) {
    super(messages[code]);
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

class McpTaskRequestFailure extends McpTaskReadError {
  readonly retryable: boolean;

  constructor(error: unknown) {
    const details = isRecord(error) ? error : undefined;
    const rawStatus = details?.status;
    const status = typeof rawStatus === 'number' && Number.isInteger(rawStatus) && rawStatus >= 100 && rawStatus <= 599
      ? rawStatus : undefined;
    const code = details?.code;
    const timeout = code === 'REQUEST_TIMEOUT';
    const failure = status === 404 || code === -32002 ? 'not_found'
      : timeout ? 'timeout' : code === 'INVALID_RESULT' ? 'invalid_task' : 'request_failed';
    super(failure, status);
    this.retryable = (failure === 'timeout' || failure === 'request_failed') &&
      (status !== undefined
        ? status === 408 || status === 429 || status >= 500
        : timeout || code === 'SEND_FAILED' || (error instanceof TypeError && error.message === 'fetch failed'));
  }
}

function parseTask(value: unknown, taskId: string): McpTaskSnapshot {
  // The official client unwraps the complete result. Input requirements are
  // part of the stored Task snapshot, so the outer result still says complete.
  if (!isRecord(value) || (value.resultType !== undefined && value.resultType !== 'complete')) {
    throw new McpTaskReadError('invalid_task');
  }
  let task: McpTaskInboxEntry;
  try {
    task = parseMcpTaskFields(value);
  } catch {
    throw new McpTaskReadError('invalid_task');
  }
  if (task.taskId !== taskId) throw new McpTaskReadError('invalid_task');
  switch (task.status) {
    case 'completed':
      if (!isRecord(value.result)) throw new McpTaskReadError('invalid_task');
      return { ...task, status: task.status, result: value.result };
    case 'failed':
      if (!isRecord(value.error)) throw new McpTaskReadError('invalid_task');
      return { ...task, status: task.status, error: value.error };
    default:
      return { ...task, status: task.status };
  }
}

async function readTask(connection: McpTaskConnection, taskId: string, options: RequestOptions): Promise<McpTaskSnapshot> {
  options.signal.throwIfAborted();
  let value: unknown;
  try {
    value = await connection.request({ method, params: { taskId } }, taskResultSchema, options);
  } catch (error) {
    options.signal.throwIfAborted();
    throw new McpTaskRequestFailure(error);
  }
  options.signal.throwIfAborted();
  return parseTask(value, taskId);
}

async function withTaskRead<Result>(
  connection: McpTaskConnection, taskId: string, options: McpTaskReadOptions, defaultTimeoutMs: number,
  read: (request: RequestOptions) => Promise<Result>,
): Promise<Result> {
  if (typeof taskId !== 'string' || taskId === '' || Buffer.byteLength(taskId) > 256 || /[\r\n\0]/u.test(taskId)) {
    throw new TypeError('Invalid MCP Task ID');
  }
  options.signal?.throwIfAborted();
  const capabilities = connection.getServerCapabilities();
  const extensions = isRecord(capabilities) ? capabilities.extensions : undefined;
  if (connection.getNegotiatedProtocolVersion() !== protocol || !isRecord(extensions) ||
      !Object.hasOwn(extensions, extension) || !isRecord(extensions[extension])) {
    throw new McpTaskReadError('unavailable');
  }
  const timeoutMs = options.timeoutMs ?? defaultTimeoutMs;
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 2 ** 31 - 1) {
    throw new RangeError('Invalid MCP Task read timeout');
  }
  const deadline = AbortSignal.timeout(timeoutMs);
  const signal = options.signal ? AbortSignal.any([options.signal, deadline]) : deadline;
  try {
    return await read({ signal, timeout: Math.min(readTimeoutMs, timeoutMs), allowInputRequired: true });
  } catch (error) {
    options.signal?.throwIfAborted();
    if (deadline.aborted) throw new McpTaskReadError('timeout');
    throw error;
  }
}

/** Read an existing Task and its retained result or execution error, when available. */
export function getMcpTask(
  connection: McpTaskConnection, taskId: string, options: McpTaskReadOptions = {},
): Promise<McpTaskSnapshot> {
  return withTaskRead(connection, taskId, options, readTimeoutMs, request => readTask(connection, taskId, request));
}

/** Observe an existing Task until it finishes or needs reviewed input. */
export function waitForMcpTask(
  connection: McpTaskConnection, taskId: string, options: McpTaskReadOptions = {},
): Promise<McpTaskWaitResult> {
  return withTaskRead(connection, taskId, options, 120_000, async request => {
    let pollMs = initialPollMs;
    let backoffMs = initialPollMs;
    for (;;) {
      let intervalMs: number;
      try {
        const task = await readTask(connection, taskId, request);
        if (task.status !== 'working') return task;
        pollMs = Math.max(minPollMs, task.pollIntervalMs ?? initialPollMs);
        backoffMs = pollMs;
        intervalMs = pollMs;
      } catch (error) {
        if (!(error instanceof McpTaskRequestFailure && error.retryable)) throw error;
        intervalMs = Math.max(pollMs, backoffMs);
        backoffMs = Math.min(backoffMs * 2, 60_000);
      }
      await delay(intervalMs, undefined, { signal: request.signal });
    }
  });
}
