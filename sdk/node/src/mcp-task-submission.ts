import { randomUUID } from 'node:crypto';
import { parseMcpTaskFields, parseMcpTaskMetadata, type McpTaskInboxEntry } from './mcp-task-inbox.js';
import { streamSse } from './sse.js';

const protocol = '2026-07-28';
const protocolMeta = 'io.modelcontextprotocol/protocolVersion';
const tasksExtension = 'io.modelcontextprotocol/tasks';
const submitMethod = 'gregale/tasks/submit';
export const boundSubmitMethod = 'gregale/tasks/submit-bound';
export const groupOwnerMethod = 'gregale/tasks/group-owner';
export const groupOwnerExtension = 'io.gregale/task-group-owner';
const groupDigest = /^[0-9a-f]{64}(?![\s\S])/u;

export type TaskGroupOwnerBinding = { groupId: string; binding: string };
const lookupMethod = 'gregale/tasks/lookup';
export const submissionKeyMeta = 'io.gregale/task-submission-key';
export const localReceiptMeta = 'io.gregale/local-task-receipt-operation';
const keyPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}(?![\s\S])/u;

const resultSchema = {
  '~standard': { version: 1 as const, vendor: 'gregale', validate: (value: unknown) => ({ value }) },
};

type TaskSubmissionRequest = {
  method: typeof submitMethod | typeof boundSubmitMethod;
  params: { name: string; arguments: McpTaskArguments; _meta: Record<string, unknown> };
};
type TaskRequest = TaskSubmissionRequest
  | { method: typeof lookupMethod; params: { submissionKey: string } }
  | { method: typeof groupOwnerMethod; params: { groupId: string } };

export interface McpTaskSubmissionConnection {
  getNegotiatedProtocolVersion(): string | undefined;
  getServerCapabilities(): unknown;
  request(
    request: TaskRequest,
    schema: typeof resultSchema,
    options: { signal: AbortSignal; timeout: number; allowInputRequired: true },
  ): Promise<unknown>;
}

export type McpTaskArgument = string | boolean | number | null | McpTaskArguments | readonly McpTaskArgument[];
export interface McpTaskArguments {
  readonly [key: string]: McpTaskArgument;
}

export interface McpTaskSubmission {
  /** Save this lowercase UUIDv7 before the first attempt. */
  readonly submissionKey: string;
  readonly toolName: string;
  readonly arguments?: McpTaskArguments;
  readonly deferFormInput?: boolean;
}

export interface McpTaskSubmissionOptions {
  readonly signal?: AbortSignal;
  /** Total deadline in milliseconds, default 30000. */
  readonly timeoutMs?: number;
}

export type McpTaskSubmissionOutcome =
  | { readonly outcome: 'accepted'; readonly task: McpTaskInboxEntry }
  | { readonly outcome: 'unconfirmed' };

const messages = {
  unavailable: 'MCP connection does not advertise the required Task submission capability on protocol 2026-07-28',
  rejected: 'MCP server rejected the keyed Task submission',
  not_found: 'MCP Task is unavailable to this caller',
  invalid_response: 'Invalid MCP Task submission lookup response',
  request_failed: 'MCP Task submission lookup failed',
} as const;

export type McpTaskSubmissionFailureCode = keyof typeof messages;

export class McpTaskSubmissionError extends Error {
  override readonly name = 'McpTaskSubmissionError';
  constructor(readonly code: McpTaskSubmissionFailureCode, readonly status?: number) {
    super(messages[code]);
  }
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function isSubmissionKey(value: unknown): value is string {
  return typeof value === 'string' && keyPattern.test(value);
}

function requireSubmissionKey(key: unknown): void {
  if (!isSubmissionKey(key)) throw new TypeError('MCP Task submission key must be a lowercase UUIDv7');
}

export function supportsTaskGroupOwnerBinding(connection: McpTaskSubmissionConnection): boolean {
  const capabilities = connection.getServerCapabilities();
  return connection.getNegotiatedProtocolVersion() === protocol && record(capabilities) &&
    record(capabilities.extensions) && Object.hasOwn(capabilities.extensions, groupOwnerExtension) &&
    record(capabilities.extensions[groupOwnerExtension]);
}

export function isTaskGroupOwnerBinding(value: unknown): value is TaskGroupOwnerBinding {
  return record(value) && Object.keys(value).length === 2 &&
    typeof value.groupId === 'string' && groupDigest.test(value.groupId) &&
    typeof value.binding === 'string' && groupDigest.test(value.binding);
}

function requireCapability(connection: McpTaskSubmissionConnection, extension: string): void {
  const capabilities = connection.getServerCapabilities();
  const extensions = record(capabilities) ? capabilities.extensions : undefined;
  if (connection.getNegotiatedProtocolVersion() !== protocol || !record(extensions) ||
      ![tasksExtension, extension].every(name => Object.hasOwn(extensions, name) && record(extensions[name]))) {
    throw new McpTaskSubmissionError('unavailable');
  }
}

function requestOptions(options: McpTaskSubmissionOptions) {
  const timeout = options.timeoutMs ?? 30_000;
  if (!Number.isInteger(timeout) || timeout < 1 || timeout > 2 ** 31 - 1) {
    throw new RangeError('Invalid MCP Task submission timeout');
  }
  const deadline = AbortSignal.timeout(timeout);
  return { timeout, signal: options.signal ? AbortSignal.any([options.signal, deadline]) : deadline, allowInputRequired: true as const };
}

function requestFailure(error: unknown): { status?: number; code?: unknown } {
  if (!record(error)) return {};
  const { status, code } = error;
  return { code, ...(typeof status === 'number' && Number.isInteger(status) && status >= 100 && status <= 599 ? { status } : {}) };
}

function snapshotArguments(value: McpTaskArgument, depth = 0): McpTaskArgument {
  if (depth < 64) {
    if (value === null || typeof value === 'string' || typeof value === 'boolean') return value;
    if (typeof value === 'number' && Number.isFinite(value) && (!Number.isInteger(value) || Number.isSafeInteger(value))) {
      return value;
    }
    if (Array.isArray(value)) return Array.from(value, child => snapshotArguments(child, depth + 1));
    if (record(value) && [null, Object.prototype].includes(Object.getPrototypeOf(value))) {
      return Object.fromEntries(Object.entries(value).map(([key, child]) => [key, snapshotArguments(child, depth + 1)]));
    }
  }
  throw new TypeError('Invalid JSON value in MCP Task arguments');
}

class AcceptedHandle {
  constructor(readonly task: McpTaskInboxEntry) {}
}

export function prepareMcpTaskSubmission(
  connection: McpTaskSubmissionConnection, submission: McpTaskSubmission,
): TaskSubmissionRequest {
  requireSubmissionKey(submission.submissionKey);
  if (typeof submission.toolName !== 'string' || !submission.toolName.trim() ||
      (submission.arguments !== undefined && !record(submission.arguments)) ||
      (submission.deferFormInput !== undefined && typeof submission.deferFormInput !== 'boolean')) {
    throw new TypeError('Invalid MCP Task submission');
  }
  requireCapability(connection, 'io.gregale/task-submission');
  const args = snapshotArguments(submission.arguments ?? {}) as McpTaskArguments;
  // Use the header marker as the SDK method. Without the fetch adapter this
  // is an unknown RPC method and cannot execute the tool as an ordinary call.
  return { method: submitMethod, params: {
    name: submission.toolName, arguments: args, _meta: {
      [submissionKeyMeta]: submission.submissionKey,
      'io.modelcontextprotocol/clientCapabilities': {
        extensions: { [tasksExtension]: {} },
        ...(submission.deferFormInput ? { elicitation: { form: {} } } : {}),
      },
    },
  } };
}

/** Send one keyed submission. An uncertain response leaves the saved key available for lookup or explicit replay. */
export async function submitMcpTask(
  connection: McpTaskSubmissionConnection, submission: McpTaskSubmission, options: McpTaskSubmissionOptions = {},
): Promise<McpTaskSubmissionOutcome> {
  return sendMcpTaskSubmission(connection, prepareMcpTaskSubmission(connection, submission), options);
}

export async function sendMcpTaskSubmission(
  connection: McpTaskSubmissionConnection, request: ReturnType<typeof prepareMcpTaskSubmission>,
  options: McpTaskSubmissionOptions,
): Promise<McpTaskSubmissionOutcome> {
  const scope = requestOptions(options);
  scope.signal.throwIfAborted();

  let value: unknown;
  try {
    value = await connection.request(request, resultSchema, scope);
  } catch (error) {
    const { status, code } = requestFailure(error);
    if (!scope.signal.aborted && ((status !== undefined && status >= 400 && status < 500 && status !== 408) ||
        (status === undefined && typeof code === 'number' && code < 0 && code !== -32603))) {
      throw new McpTaskSubmissionError('rejected', status);
    }
    return { outcome: 'unconfirmed' };
  }
  if (record(value)) {
    if (value.handle instanceof AcceptedHandle) return { outcome: 'accepted', task: value.handle.task };
    if (value.isError === true) throw new McpTaskSubmissionError('rejected');
  }
  return { outcome: 'unconfirmed' };
}

/** Read metadata for the caller's retained Task, including after the submission window ends. */
export async function lookupMcpTaskBySubmissionKey(
  connection: McpTaskSubmissionConnection, submissionKey: string, options: McpTaskSubmissionOptions = {},
): Promise<McpTaskInboxEntry> {
  requireSubmissionKey(submissionKey);
  requireCapability(connection, 'io.gregale/task-submission-lookup');
  const scope = requestOptions(options);
  scope.signal.throwIfAborted();
  let value: unknown;
  try {
    value = await connection.request({ method: lookupMethod, params: { submissionKey } }, resultSchema, scope);
  } catch (error) {
    scope.signal.throwIfAborted();
    const { status, code } = requestFailure(error);
    throw new McpTaskSubmissionError(status === 404 || code === -32002 || code === -32004 ? 'not_found' : 'request_failed', status);
  }
  scope.signal.throwIfAborted();
  try {
    if (!record(value)) throw new McpTaskSubmissionError('invalid_response');
    return parseMcpTaskMetadata({ resultType: 'complete', ...value });
  } catch {
    throw new McpTaskSubmissionError('invalid_response');
  }
}

function parseGroupOwnerProof(value: unknown): string {
  if (!record(value) || (value.resultType !== undefined && value.resultType !== 'complete') ||
      typeof value.binding !== 'string' || !groupDigest.test(value.binding)) {
    throw new McpTaskSubmissionError('invalid_response');
  }
  return value.binding;
}

export async function requestTaskGroupOwnerBinding(
  connection: McpTaskSubmissionConnection, groupId: string, options: McpTaskSubmissionOptions,
): Promise<string> {
  requireCapability(connection, groupOwnerExtension);
  const scope = requestOptions(options);
  scope.signal.throwIfAborted();
  let result: unknown;
  try {
    result = await connection.request({ method: groupOwnerMethod, params: { groupId } }, resultSchema, scope);
  } catch (error) {
    scope.signal.throwIfAborted();
    const { status } = requestFailure(error);
    throw new McpTaskSubmissionError('request_failed', status);
  }
  scope.signal.throwIfAborted();
  return parseGroupOwnerProof(result);
}

/** Check the exact bearer on the pending HTTP request before journaling its first send. */
export async function verifyTaskGroupOwnerForSend(
  fetcher: typeof globalThis.fetch, endpoint: string, originalHeaders: Headers,
  owner: TaskGroupOwnerBinding, toolName: string, signal: AbortSignal,
): Promise<void> {
  const headers = new Headers(originalHeaders);
  headers.set('mcp-method', groupOwnerMethod);
  headers.delete('mcp-name');
  headers.delete('content-length');
  headers.set('accept', 'application/json');
  headers.set('content-type', 'application/json');
  const id = randomUUID();
  try {
    const response = await fetcher(endpoint, {
      method: 'POST', headers, redirect: 'manual', signal,
      body: JSON.stringify({ jsonrpc: '2.0', id, method: groupOwnerMethod, params: {
        ...owner, toolName, _meta: { [protocolMeta]: protocol },
      } }),
    });
    if (!response.ok) {
      void response.body?.cancel().catch(() => undefined);
      throw new McpTaskSubmissionError('rejected', response.status);
    }
    let size = 0;
    const body = response.body?.pipeThrough(new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, output) {
        size += chunk.byteLength;
        if (size > 4096) throw new McpTaskSubmissionError('invalid_response');
        output.enqueue(chunk);
      },
    }));
    const reply: unknown = await new Response(body).json();
    signal.throwIfAborted();
    if (!isReplyFor(reply, id) || Object.hasOwn(reply, 'error') || parseGroupOwnerProof(reply.result) !== owner.binding) {
      throw new McpTaskSubmissionError('invalid_response');
    }
  } catch (error) {
    signal.throwIfAborted();
    if (error instanceof McpTaskSubmissionError) throw error;
    throw new McpTaskSubmissionError('request_failed');
  }
}

function nameHeader(name: string): string {
  if (name.trim() === name && !/[^\x20-\x7e]/u.test(name) && !(name.startsWith('=?base64?') && name.endsWith('?='))) {
    return name;
  }
  return `=?base64?${Buffer.from(name).toString('base64')}?=`;
}

function isReplyFor(reply: unknown, id: string | number): reply is Record<string, unknown> {
  return record(reply) && reply.jsonrpc === '2.0' && reply.id === id &&
    (Object.hasOwn(reply, 'result') || Object.hasOwn(reply, 'error'));
}

async function readSubmissionStream(response: Response, id: string | number, signal?: AbortSignal): Promise<unknown> {
  if (!response.body) throw new TypeError('Missing keyed MCP Task response body');
  let size = 0;
  const body = response.body.pipeThrough(new TransformStream<Uint8Array, Uint8Array>({
    transform(chunk, controller) {
      size += chunk.byteLength;
      if (size > 1 << 20) throw new TypeError('Keyed MCP Task response exceeds the size limit');
      controller.enqueue(chunk);
    },
  }));
  try {
    for await (const event of streamSse(new Response(body), signal)) {
      if (event.event !== 'message' || !event.data) continue;
      const reply: unknown = JSON.parse(event.data);
      if (isReplyFor(reply, id)) return reply;
    }
    throw new TypeError('Missing keyed MCP Task acknowledgement');
  } finally {
    void body.cancel().catch(() => undefined);
  }
}

function adaptSubmissionReply(response: Response, id: string | number, signal?: AbortSignal): Response {
  const mediaType = response.headers.get('content-type')?.split(';', 1)[0]?.trim().toLowerCase();
  if (!response.ok || (mediaType !== 'application/json' && mediaType !== 'text/event-stream')) return response;

  let read = response.json.bind(response);
  if (mediaType === 'text/event-stream') {
    // Keyed submission only returns a handle. Read the matching terminal
    // frame from the Node starter and present it through the JSON codec.
    read = () => readSubmissionStream(response, id, signal);
    const headers = new Headers(response.headers);
    headers.set('content-type', 'application/json');
    headers.delete('content-length');
    Object.defineProperty(response, 'headers', { value: headers });
  }

  Object.defineProperty(response, 'json', { value: async () => {
    const reply: unknown = await read();
    if (!isReplyFor(reply, id) || Object.hasOwn(reply, 'error') ||
        !record(reply.result) || reply.result.resultType !== 'task' || reply.result.isError === true) return reply;
    // Keep the handle in a private value that an ordinary tool result cannot forge.
    const task = parseMcpTaskFields(reply.result);
    return { ...reply, result: { resultType: 'complete', handle: new AcceptedHandle(task) } };
  } });
  return response;
}

type ReceiptSubmissionGuard = (
  operationId: string, params: Record<string, unknown>, metadata: Record<string, unknown>, headers: Headers,
) => Promise<void>;

export function createGuardedMcpTaskSubmissionFetch(
  fetcher: typeof globalThis.fetch = globalThis.fetch, guard?: ReceiptSubmissionGuard,
): typeof globalThis.fetch {
  return async (input, init) => {
    const request = input instanceof Request ? input : undefined;
    const headers = new Headers(init?.headers ?? request?.headers);
    const method = headers.get('mcp-method');
    if ((init?.method ?? request?.method)?.toUpperCase() !== 'POST' || (method !== submitMethod && method !== boundSubmitMethod)) {
      return fetcher(input, init);
    }

    const body = init?.body ?? await request?.clone().text();
    const message: unknown = typeof body === 'string' ? JSON.parse(body) : undefined;
    if (!record(message) || message.jsonrpc !== '2.0' || message.method !== method ||
        (typeof message.id !== 'number' && typeof message.id !== 'string') ||
        !record(message.params) || typeof message.params.name !== 'string' || !message.params.name.trim() ||
        !record(message.params._meta) || !isSubmissionKey(message.params._meta[submissionKeyMeta]) ||
        headers.get('mcp-protocol-version') !== protocol || message.params._meta[protocolMeta] !== protocol) {
      throw new TypeError('Invalid keyed MCP Task request envelope');
    }
    const metadata = message.params._meta;
    const owner = metadata[groupOwnerExtension];
    if (method === boundSubmitMethod ? !isTaskGroupOwnerBinding(owner) : owner !== undefined) {
      throw new TypeError('Invalid MCP Task group owner binding');
    }
    if (Object.hasOwn(metadata, localReceiptMeta)) {
      const operationId = metadata[localReceiptMeta];
      if (!guard || typeof operationId !== 'string') throw new TypeError('Unbound MCP Task receipt operation');
      await guard(operationId, message.params, metadata, headers);
      delete metadata[localReceiptMeta];
    }
    (init?.signal ?? request?.signal)?.throwIfAborted();
    headers.set('mcp-name', nameHeader(message.params.name));
    headers.delete('content-length');
    const response = await fetcher(input, { ...init, headers,
      body: JSON.stringify({ ...message, method: method === boundSubmitMethod ? method : 'tools/call' }),
    });
    return adaptSubmissionReply(response, message.id, init?.signal ?? request?.signal);
  };
}

/** Pass to the official Streamable HTTP transport's fetch option for keyed Task submission. */
export function createMcpTaskSubmissionFetch(fetcher: typeof globalThis.fetch = globalThis.fetch): typeof globalThis.fetch {
  return createGuardedMcpTaskSubmissionFetch(fetcher);
}
