import { type McpTaskInboxEntry, parseMcpTaskMetadata } from './mcp-task-inbox.js';
import { isTaskReviewObject, maxReviewJSONDepth, McpTaskReviewNumber } from './mcp-task-review-json.js';

type Method = 'gregale/tasks/input-review' | 'gregale/tasks/respond-reviewed' | 'gregale/tasks/check-reviewed-response';

const completeResult = {
  '~standard': { version: 1 as const, vendor: 'gregale', validate: (value: unknown) => ({ value }) },
};

export interface McpTaskInputReviewConnection {
  getNegotiatedProtocolVersion(): string | undefined;
  getServerCapabilities(): unknown;
  request(
    request: { method: Method; params: { taskId: string; reviewRevision?: string; responses?: McpTaskReviewAnswers } },
    resultSchema: typeof completeResult,
    options: { signal: AbortSignal; timeout: number; allowInputRequired: true },
  ): Promise<unknown>;
}

export type McpTaskReviewJSON = string | boolean | null | number | McpTaskReviewNumber |
  readonly McpTaskReviewJSON[] | McpTaskReviewJSONObject;
export interface McpTaskReviewJSONObject {
  readonly [key: string]: McpTaskReviewJSON;
}

export interface McpTaskReviewForm {
  readonly message: string;
  readonly schema: McpTaskReviewJSONObject;
}

export interface McpTaskInputReview {
  readonly task: McpTaskInboxEntry;
  readonly reviewRevision?: string;
  readonly forms: Readonly<Record<string, McpTaskReviewForm>>;
}

export type McpTaskReviewAnswer = { readonly action: 'accept'; readonly content: McpTaskReviewJSONObject } |
  { readonly action: 'decline' | 'cancel' };
export type McpTaskReviewAnswers = Readonly<Record<string, McpTaskReviewAnswer>>;

export type McpTaskReviewOutcome =
  | { readonly outcome: 'applied' | 'stale'; readonly task: McpTaskInboxEntry }
  | { readonly outcome: 'unconfirmed'; readonly task?: McpTaskInboxEntry };

export interface McpTaskInputReviewOptions {
  readonly signal?: AbortSignal;
  /** Total budget for the operation, including any confirmation. Defaults to 30000 ms. */
  readonly timeoutMs?: number;
}

const messages = {
  unavailable: 'MCP connection does not advertise Task input review on protocol 2026-07-28',
  invalid_review: 'Invalid MCP Task input review',
  invalid_answers: 'Invalid MCP Task reviewed answers',
  exact_transport_required: 'Use createMcpTaskReviewFetch to preserve exact form numbers',
  request_failed: 'MCP Task input review request failed',
} as const;

export type McpTaskInputReviewFailureCode = keyof typeof messages;
export class McpTaskInputReviewError extends Error {
  override readonly name = 'McpTaskInputReviewError';
  constructor(readonly code: McpTaskInputReviewFailureCode, readonly status?: number) {
    super(messages[code]);
  }
}

const revisions = /^[0-9a-f]{64}(?![\s\S])/u;
const connections = new WeakMap<McpTaskInputReview, McpTaskInputReviewConnection>();
const maxForms = 16;
const maxAnswerBytes = 256 << 10;

function requireReviewCapability(connection: McpTaskInputReviewConnection): void {
  const capabilities = connection.getServerCapabilities();
  const extensions = isTaskReviewObject(capabilities) ? capabilities.extensions : undefined;
  if (connection.getNegotiatedProtocolVersion() !== '2026-07-28' ||
      !isTaskReviewObject(extensions) ||
      !['io.modelcontextprotocol/tasks', 'io.gregale/task-input-review'].every(name => Object.hasOwn(extensions, name) &&
        isTaskReviewObject(extensions[name]))) {
    throw new McpTaskInputReviewError('unavailable');
  }
}

function operation(options: McpTaskInputReviewOptions): { signal: AbortSignal; timeout: number; allowInputRequired: true } {
  const timeout = options.timeoutMs ?? 30_000;
  if (!Number.isInteger(timeout) || timeout < 1 || timeout > 2 ** 31 - 1) throw new RangeError('Invalid MCP Task review timeout');
  const deadline = AbortSignal.timeout(timeout);
  return { signal: options.signal ? AbortSignal.any([options.signal, deadline]) : deadline, timeout, allowInputRequired: true };
}

function snapshotJSON(value: unknown, forForm: boolean, depth = 0): McpTaskReviewJSON {
  const error = forForm ? 'invalid_review' : 'invalid_answers';
  if (depth > maxReviewJSONDepth) throw new McpTaskInputReviewError(error);
  if (value === null || typeof value === 'string' || typeof value === 'boolean' || value instanceof McpTaskReviewNumber) return value;
  if (typeof value === 'number') {
    if (forForm) throw new McpTaskInputReviewError('exact_transport_required');
    if (Number.isFinite(value) && (!Number.isInteger(value) || Number.isSafeInteger(value))) return value;
    throw new McpTaskInputReviewError(error);
  }
  if (Array.isArray(value)) return Object.freeze(Array.from(value, child => snapshotJSON(child, forForm, depth + 1)));
  if (!isTaskReviewObject(value)) throw new McpTaskInputReviewError(error);
  return Object.freeze(Object.fromEntries(Object.entries(value).map(([key, child]) => [key, snapshotJSON(child, forForm, depth + 1)])));
}

function taskResult(value: unknown, taskId: string): { result: Record<string, unknown>; task: McpTaskInboxEntry } {
  if (!isTaskReviewObject(value) || (value.resultType !== undefined && value.resultType !== 'complete')) {
    throw new McpTaskInputReviewError('invalid_review');
  }
  let task: McpTaskInboxEntry;
  try {
    task = parseMcpTaskMetadata(value.task);
  } catch {
    throw new McpTaskInputReviewError('invalid_review');
  }
  if (task.taskId !== taskId) throw new McpTaskInputReviewError('invalid_review');
  return { result: value, task: Object.freeze(task) };
}

function parseForm(id: string, form: unknown): readonly [string, McpTaskReviewForm] {
  if (id.length === 0 || Buffer.byteLength(id) > 256 || !isTaskReviewObject(form) || form.method !== 'elicitation/create' ||
      !isTaskReviewObject(form.params)) throw new McpTaskInputReviewError('invalid_review');
  const { mode, message = '', requestedSchema } = form.params;
  if ((mode !== undefined && mode !== 'form') || typeof message !== 'string' || Buffer.byteLength(message) > 16 << 10 ||
      !isTaskReviewObject(requestedSchema) || requestedSchema.type !== 'object' ||
      !isTaskReviewObject(requestedSchema.properties) || Object.keys(requestedSchema.properties).length > 100) {
    throw new McpTaskInputReviewError('invalid_review');
  }
  const schema = snapshotJSON(requestedSchema, true) as McpTaskReviewJSONObject;
  if (Buffer.byteLength(JSON.stringify(schema)) > (64 << 10)) throw new McpTaskInputReviewError('invalid_review');
  return [id, Object.freeze({ message, schema })];
}

function parseReview(value: unknown, taskId: string): McpTaskInputReview {
  const { task, result } = taskResult(value, taskId);
  const { reviewRevision, inputRequests } = result;
  if (reviewRevision === undefined && (inputRequests === undefined ||
      (isTaskReviewObject(inputRequests) && Object.keys(inputRequests).length === 0))) {
    return Object.freeze({ task, forms: Object.freeze({}) });
  }
  if (typeof reviewRevision !== 'string' || !revisions.test(reviewRevision) ||
      task.status !== 'input_required' || !isTaskReviewObject(inputRequests)) {
    throw new McpTaskInputReviewError('invalid_review');
  }
  const forms = Object.entries(inputRequests);
  if (forms.length < 1 || forms.length > maxForms) throw new McpTaskInputReviewError('invalid_review');
  return Object.freeze({ task, reviewRevision, forms: Object.freeze(Object.fromEntries(forms.map(([id, form]) => parseForm(id, form)))) });
}

function snapshotAnswers(review: McpTaskInputReview, answers: McpTaskReviewAnswers): McpTaskReviewAnswers {
  if (!isTaskReviewObject(answers)) throw new McpTaskInputReviewError('invalid_answers');
  const entries = Object.entries(answers);
  if (entries.length !== Object.keys(review.forms).length) throw new McpTaskInputReviewError('invalid_answers');
  const snapshot = Object.freeze(Object.fromEntries(entries.map(([id, answer]): [string, McpTaskReviewAnswer] => {
    if (!Object.hasOwn(review.forms, id) || !isTaskReviewObject(answer)) throw new McpTaskInputReviewError('invalid_answers');
    if (answer.action === 'accept' && Object.keys(answer).length === 2 && isTaskReviewObject(answer.content)) {
      return [id, Object.freeze({ action: 'accept', content: snapshotJSON(answer.content, false) as McpTaskReviewJSONObject })];
    }
    if ((answer.action === 'decline' || answer.action === 'cancel') && Object.keys(answer).length === 1) {
      return [id, Object.freeze({ action: answer.action })];
    }
    throw new McpTaskInputReviewError('invalid_answers');
  })));
  try {
    if (Buffer.byteLength(JSON.stringify(snapshot)) <= maxAnswerBytes) return snapshot;
  } catch {
    // Serialization can fail if the runtime cannot encode exact JSON numbers.
  }
  throw new McpTaskInputReviewError('invalid_answers');
}

function httpStatus(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null || !('status' in error)) return undefined;
  const status: unknown = error.status;
  return typeof status === 'number' && Number.isInteger(status) && status >= 100 && status <= 599 ? status : undefined;
}

function definiteRejection(error: unknown): boolean {
  const status = httpStatus(error);
  if (status !== undefined) return status >= 300 && status < 500 && status !== 408;
  const code = typeof error === 'object' && error !== null && 'code' in error ? error.code : undefined;
  return typeof code === 'number' && Number.isInteger(code) && code < 0 && code !== -32603;
}

/** Fetch current form-only input and its revision from the authenticated connection. */
export async function getTaskInputReview(
  connection: McpTaskInputReviewConnection, taskId: string, options: McpTaskInputReviewOptions = {},
): Promise<McpTaskInputReview> {
  if (typeof taskId !== 'string' || taskId.length === 0 || Buffer.byteLength(taskId) > 256 || /[\r\n\0]/u.test(taskId)) {
    throw new McpTaskInputReviewError('invalid_review');
  }
  requireReviewCapability(connection);
  const scope = operation(options);
  scope.signal.throwIfAborted();
  let value: unknown;
  try {
    value = await connection.request({ method: 'gregale/tasks/input-review', params: { taskId } }, completeResult, scope);
  } catch (error) {
    scope.signal.throwIfAborted();
    throw new McpTaskInputReviewError('request_failed', httpStatus(error));
  }
  scope.signal.throwIfAborted();
  const review = parseReview(value, taskId);
  connections.set(review, connection);
  return review;
}

/** Submit the reviewed form set once and confirm an uncertain reply by reading retained evidence. */
export async function respondToReviewedTaskInput(
  connection: McpTaskInputReviewConnection, review: McpTaskInputReview, answers: McpTaskReviewAnswers,
  options: McpTaskInputReviewOptions = {},
): Promise<McpTaskReviewOutcome> {
  if (connections.get(review) !== connection || !review.reviewRevision) throw new McpTaskInputReviewError('invalid_review');
  requireReviewCapability(connection);
  const scope = operation(options);
  const params = { taskId: review.task.taskId, reviewRevision: review.reviewRevision, responses: snapshotAnswers(review, answers) };
  scope.signal.throwIfAborted();

  try {
    const value = await connection.request({ method: 'gregale/tasks/respond-reviewed', params }, completeResult, scope);
    const { result, task } = taskResult(value, params.taskId);
    if (result.outcome === 'applied' || result.outcome === 'stale') return { outcome: result.outcome, task };
  } catch (error) {
    if (definiteRejection(error)) throw new McpTaskInputReviewError('request_failed', httpStatus(error));
  }

  if (!scope.signal.aborted) {
    try {
      const value = await connection.request({ method: 'gregale/tasks/check-reviewed-response', params }, completeResult, scope);
      const { result, task } = taskResult(value, params.taskId);
      if (result.outcome === 'applied' || result.outcome === 'unconfirmed') return { outcome: result.outcome, task };
    } catch {
      // The write may still have committed. Confirmation is a read, so an
      // unavailable check leaves the outcome unresolved.
    }
  }
  return { outcome: 'unconfirmed' };
}
