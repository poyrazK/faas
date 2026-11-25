import { setTimeout as delay } from 'node:timers/promises';

const method = 'gregale/tasks/inbox';
const protocol = '2026-07-28';
const extensions = ['io.modelcontextprotocol/tasks', 'io.gregale/task-inbox'];
const maxPages = 64;
const maxPageSize = 50;
const cursorPattern = /^[A-Za-z0-9_-]{1,128}\.[A-Za-z0-9_-]{43}(?![\s\S])/u;
const timestampPattern = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})(?![\s\S])/u;

const nextActions = {
  working: 'poll_status',
  input_required: 'provide_input',
  completed: 'read_result',
  failed: 'none',
  cancelled: 'none',
} as const;

export type McpTaskInboxStatus = keyof typeof nextActions;

export interface McpTaskInboxEntry {
  readonly taskId: string;
  readonly status: McpTaskInboxStatus;
  readonly createdAt: string;
  readonly lastUpdatedAt: string;
  readonly ttlMs: number | null;
  readonly pollIntervalMs?: number;
  readonly nextAction: typeof nextActions[McpTaskInboxStatus];
}

export interface McpTaskInboxPage {
  readonly tasks: readonly McpTaskInboxEntry[];
  readonly nextCursor?: string;
}

export interface McpTaskInboxListOptions {
  readonly status?: McpTaskInboxStatus;
  /** Page size from 1 to 50, default 20. */
  readonly limit?: number;
  /** Per-page deadline in milliseconds, default 30000. */
  readonly timeoutMs?: number;
  readonly signal?: AbortSignal;
  readonly cursor?: string;
}

export interface McpTaskInboxWatchOptions extends Omit<McpTaskInboxListOptions, 'cursor' | 'timeoutMs'> {
  /** Deadline for one complete scan in milliseconds, default 30000. */
  readonly timeoutMs?: number;
  /** Delay between scans from 1000 to 3600000 milliseconds, default 30000. */
  readonly intervalMs?: number;
}

/** The official MCP Client supplies this interface after connecting. */
export interface McpTaskInboxConnection {
  getNegotiatedProtocolVersion(): string | undefined;
  getServerCapabilities(): unknown;
  request(
    request: { method: typeof method; params: Query },
    resultSchema: InboxResultSchema,
    options: { signal: AbortSignal; timeout: number; allowInputRequired: true },
  ): Promise<unknown>;
}

// Validate the result against its query below, after the official client decodes it.
const inboxResultSchema = {
  '~standard': { version: 1 as const, vendor: 'gregale', validate: (value: unknown) => ({ value }) },
};
type InboxResultSchema = typeof inboxResultSchema;
type Query = { limit: number; status?: McpTaskInboxStatus; cursor?: string };

const messages = {
  unavailable: 'MCP connection does not advertise the Task inbox on protocol 2026-07-28',
  invalid_page: 'Invalid MCP Task inbox page',
  scan_limit: 'MCP Task inbox scan exceeded 64 pages',
  timeout: 'MCP Task inbox read deadline exceeded',
  request_failed: 'MCP Task inbox request failed',
} as const;

export type McpTaskInboxFailureCode = keyof typeof messages;

/** Inbox failures expose a bounded code and optional HTTP status without server text. */
export class McpTaskInboxError extends Error {
  override readonly name = 'McpTaskInboxError';
  constructor(readonly code: McpTaskInboxFailureCode, readonly status?: number) {
    super(messages[code]);
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isStatus(value: unknown): value is McpTaskInboxStatus {
  return typeof value === 'string' && Object.hasOwn(nextActions, value);
}

function isCursor(value: unknown): value is string {
  return typeof value === 'string' && cursorPattern.test(value);
}

function isTimestamp(value: unknown): value is string {
  return typeof value === 'string' && timestampPattern.test(value) && Number.isFinite(Date.parse(value));
}

function isInteger(value: unknown, min: number, max: number): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= min && value <= max;
}

function integerOption(value: number, name: string, min: number, max: number): number {
  if (!isInteger(value, min, max)) throw new TypeError(`${name} must be an integer from ${min} through ${max}`);
  return value;
}

function taskInboxQuery(options: McpTaskInboxListOptions): Query {
  const { status, cursor } = options;
  if (status !== undefined && !isStatus(status)) throw new TypeError('Invalid MCP Task inbox status');
  if (cursor !== undefined && !isCursor(cursor)) throw new TypeError('Invalid MCP Task inbox cursor');
  return {
    limit: integerOption(options.limit ?? 20, 'limit', 1, maxPageSize),
    ...(status === undefined ? {} : { status }),
    ...(cursor === undefined ? {} : { cursor }),
  };
}

const payloadFields = new Set(['result', 'error', 'inputrequests', 'statusmessage', 'arguments']);

export function parseMcpTaskMetadata(value: unknown): McpTaskInboxEntry {
  if (!isRecord(value) || value.resultType !== 'complete' ||
      Object.keys(value).some(key => payloadFields.has(key.toLowerCase()))) {
    throw new McpTaskInboxError('invalid_page');
  }
  return parseMcpTaskFields(value);
}

/** Project the shared metadata fields from an already checked Task response. */
export function parseMcpTaskFields(value: Record<string, unknown>): McpTaskInboxEntry {
  const { taskId, status, createdAt, lastUpdatedAt, ttlMs, pollIntervalMs } = value;
  if (typeof taskId !== 'string' || taskId === '' || Buffer.byteLength(taskId, 'utf8') > 256 || /[\r\n\0]/u.test(taskId) ||
      !isStatus(status) ||
      !isTimestamp(createdAt) || !isTimestamp(lastUpdatedAt) ||
      (ttlMs !== null && !isInteger(ttlMs, 0, Number.MAX_SAFE_INTEGER)) ||
      (pollIntervalMs !== undefined && !isInteger(pollIntervalMs, 0, 86_400_000))) {
    throw new McpTaskInboxError('invalid_page');
  }
  return {
    taskId, status, createdAt, lastUpdatedAt, ttlMs, nextAction: nextActions[status],
    ...(pollIntervalMs === undefined ? {} : { pollIntervalMs }),
  };
}

function parsePage(value: unknown, query: Query): McpTaskInboxPage {
  // The connected SDK validates and removes the outer complete discriminator.
  // A manual input-required response retains its own discriminator.
  if (!isRecord(value) || (value.resultType !== undefined && value.resultType !== 'complete') ||
      !Array.isArray(value.tasks) || value.tasks.length > query.limit) {
    throw new McpTaskInboxError('invalid_page');
  }
  const tasks = value.tasks.map(parseMcpTaskMetadata);
  if (query.status !== undefined && tasks.some(task => task.status !== query.status)) {
    throw new McpTaskInboxError('invalid_page');
  }
  if (new Set(tasks.map(task => task.taskId)).size !== tasks.length) throw new McpTaskInboxError('invalid_page');
  const cursor = value.nextCursor;
  if (cursor === undefined || cursor === '') return { tasks };
  if (!isCursor(cursor) || cursor === query.cursor || tasks.length !== query.limit) {
    throw new McpTaskInboxError('invalid_page');
  }
  return { tasks, nextCursor: cursor };
}

// The upstream SDK exposes HTTP status and stable transport codes. Its error
// message and data can include endpoint response text.
class TaskInboxRequestFailure extends McpTaskInboxError {
  readonly retryable: boolean;

  constructor(error: unknown) {
    const details = isRecord(error) ? error : undefined;
    const status = isInteger(details?.status, 100, 599) ? details.status : undefined;
    const code = details?.code;
    const timeout = code === 'REQUEST_TIMEOUT';
    super(timeout ? 'timeout' : code === 'INVALID_RESULT' ? 'invalid_page' : 'request_failed', status);
    this.retryable = status !== undefined
      ? status === 408 || status === 429 || status >= 500
      : timeout || code === 'SEND_FAILED' || (error instanceof TypeError && error.message === 'fetch failed');
  }
}

async function readPage(connection: McpTaskInboxConnection, query: Query, signal: AbortSignal, timeoutMs: number): Promise<McpTaskInboxPage> {
  signal.throwIfAborted();
  const capabilities = connection.getServerCapabilities();
  const advertised = isRecord(capabilities) ? capabilities.extensions : undefined;
  if (connection.getNegotiatedProtocolVersion() !== protocol || !isRecord(advertised) ||
      !extensions.every(name => Object.hasOwn(advertised, name) && isRecord(advertised[name]))) {
    throw new McpTaskInboxError('unavailable');
  }
  let result: unknown;
  try {
    result = await connection.request({ method, params: query }, inboxResultSchema, {
      signal, timeout: timeoutMs, allowInputRequired: true,
    });
  } catch (error) {
    signal.throwIfAborted();
    throw new TaskInboxRequestFailure(error);
  }
  signal.throwIfAborted();
  return parsePage(result, query);
}

function withDeadline(signal: AbortSignal | undefined, timeoutMs: number): AbortSignal {
  const deadline = AbortSignal.timeout(timeoutMs);
  return signal ? AbortSignal.any([signal, deadline]) : deadline;
}

function inboxTimeout(options: McpTaskInboxListOptions): number {
  return integerOption(options.timeoutMs ?? 30_000, 'timeoutMs', 1, 2 ** 31 - 1);
}

/** Read one metadata page from the connected caller's retained Task inbox. */
export async function listTaskInbox(connection: McpTaskInboxConnection, options: McpTaskInboxListOptions = {}): Promise<McpTaskInboxPage> {
  const timeoutMs = inboxTimeout(options);
  return readPage(connection, taskInboxQuery(options), withDeadline(options.signal, timeoutMs), timeoutMs);
}

async function scanInbox(connection: McpTaskInboxConnection, query: Query, signal: AbortSignal, timeoutMs: number): Promise<Map<string, McpTaskInboxEntry>> {
  const tasks = new Map<string, McpTaskInboxEntry>();
  const cursors = new Set<string>();
  let cursor: string | undefined;
  for (let page = 0; page < maxPages; page++) {
    const pageQuery = cursor === undefined ? query : { ...query, cursor };
    const result = await readPage(connection, pageQuery, signal, timeoutMs);
    for (const task of result.tasks) tasks.set(task.taskId, task);
    cursor = result.nextCursor;
    if (cursor === undefined) return tasks;
    if (cursors.has(cursor)) throw new McpTaskInboxError('invalid_page');
    cursors.add(cursor);
  }
  throw new McpTaskInboxError('scan_limit');
}

async function waitForNextScan(intervalMs: number, signal: AbortSignal): Promise<void> {
  try {
    await delay(intervalMs, undefined, { signal });
  } catch (error) {
    signal.throwIfAborted();
    throw error;
  }
}

async function* watchInbox(
  connection: McpTaskInboxConnection, query: Query, signal: AbortSignal, timeoutMs: number, intervalMs: number,
): AsyncGenerator<McpTaskInboxEntry, void, unknown> {
  let previous = new Map<string, string>();
  let backoff = intervalMs;
  while (true) {
    const scanSignal = withDeadline(signal, timeoutMs);
    let snapshot: Map<string, McpTaskInboxEntry>;
    try {
      snapshot = await scanInbox(connection, query, scanSignal, timeoutMs);
    } catch (error) {
      signal.throwIfAborted();
      if (!scanSignal.aborted && !(error instanceof TaskInboxRequestFailure && error.retryable)) throw error;
      await waitForNextScan(backoff, signal);
      backoff = Math.min(2 * backoff, Math.max(intervalMs, 60_000));
      continue;
    }
    const current = new Map<string, string>();
    for (const task of snapshot.values()) {
      signal.throwIfAborted();
      const fingerprint = JSON.stringify(task);
      current.set(task.taskId, fingerprint);
      if (previous.get(task.taskId) !== fingerprint) yield task;
    }
    previous = current;
    backoff = intervalMs;
    await waitForNextScan(intervalMs, signal);
  }
}

/** Observe new or changed entries from complete scans. Abort or close the iterator to stop waiting. */
export function watchTaskInbox(
  connection: McpTaskInboxConnection, options: McpTaskInboxWatchOptions = {},
): AsyncGenerator<McpTaskInboxEntry, void, unknown> {
  const query = taskInboxQuery({ status: options.status, limit: options.limit });
  const timeoutMs = inboxTimeout(options);
  const intervalMs = integerOption(options.intervalMs ?? 30_000, 'intervalMs', 1_000, 3_600_000);
  const stop = new AbortController();
  const signal = options.signal ? AbortSignal.any([options.signal, stop.signal]) : stop.signal;
  const iterator = watchInbox(connection, query, signal, timeoutMs, intervalMs);
  // Native generator.return() queues behind next(). Cancel the outstanding
  // read or retry delay first, as with the SDK deployment iterators.
  return {
    next: (...args) => iterator.next(...args),
    return(value) { stop.abort(); return iterator.return(value); },
    throw(reason) { stop.abort(reason); return iterator.throw(reason); },
    [Symbol.asyncIterator]() { return this; },
  };
}
