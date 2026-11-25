import { createHash, createHmac, randomBytes, randomUUID, timingSafeEqual } from 'node:crypto';
import { constants } from 'node:fs';
import { link, open, realpath, rename, unlink } from 'node:fs/promises';
import { basename, dirname, join, resolve } from 'node:path';
import { jobValueHash } from './job-runtime.js';
import { isMcpTaskId } from './mcp-task-inbox.js';
import { taskWatchTransport } from './mcp-task-watch-transport.js';
import { getMcpTask, type McpTaskConnection, type McpTaskSnapshot } from './mcp-task-result.js';
import {
  createGuardedMcpTaskSubmissionFetch, isSubmissionKey, localReceiptMeta, lookupMcpTaskBySubmissionKey,
  prepareMcpTaskSubmission, sendMcpTaskSubmission, submissionKeyMeta,
  type McpTaskSubmission, type McpTaskSubmissionConnection, type McpTaskSubmissionOptions, type McpTaskSubmissionOutcome,
} from './mcp-task-submission.js';

const maxReceiptBytes = 4096;
type SubmissionGuard = NonNullable<Parameters<typeof createGuardedMcpTaskSubmissionFetch>[1]>;
type TransportBinding = {
  endpointSha256: string;
  operations: Map<string, SubmissionGuard>;
};

const transports = new WeakMap<object, TransportBinding>();
const submissionWindowMs = 60_000;
const receiptMethods = new Set(['gregale/tasks/submit', 'gregale/tasks/lookup', 'tasks/get']);
const sha256Pattern = /^[0-9a-f]{64}(?![\s\S])/u;

export type McpTaskReceiptConnection = McpTaskConnection & McpTaskSubmissionConnection & {
  readonly transport: object | undefined;
};
export type McpTaskReceiptSubmission = Omit<McpTaskSubmission, 'submissionKey'>;
export type McpTaskReceiptSubmissionOutcome =
  | (Extract<McpTaskSubmissionOutcome, { outcome: 'accepted' }> & { readonly taskIdRecorded: boolean })
  | Extract<McpTaskSubmissionOutcome, { outcome: 'unconfirmed' }>;

type ReceiptFields = {
  endpointSha256: string;
  submissionKey: string;
  taskId?: string;
};
type ReadOnlyReceipt = ReceiptFields & { version: 1 };
type RetryReceipt = ReceiptFields & { version: 2; requestSha256: string; credentialProof: string };
type Receipt = ReadOnlyReceipt | RetryReceipt;

const messages = {
  invalid_endpoint: 'MCP Task receipts require an HTTP or HTTPS endpoint without credentials or a fragment',
  unbound_connection: 'Connect using createMcpTaskReceiptTransport before using a Task receipt',
  endpoint_mismatch: 'MCP Task receipt does not match the selected endpoint',
  invalid_receipt: 'Invalid or non-private MCP Task receipt',
  receipt_exists: 'MCP Task receipt already exists. Resume it to check the saved submission',
  receipt_io: 'Could not read or write the MCP Task receipt',
  retry_unavailable: 'MCP Task receipt does not have a credential binding for retry',
  retry_window_closed: 'MCP Task submission is outside its retry window',
  request_mismatch: 'MCP Task retry requires the original tool, arguments, and deferred-input setting',
  task_mismatch: 'The returned MCP Task ID does not match the receipt',
  credential_mismatch: 'MCP Task retry requires the original bearer credential',
} as const;
export type McpTaskReceiptFailureCode = keyof typeof messages;

export class McpTaskReceiptError extends Error {
  override readonly name = 'McpTaskReceiptError';
  constructor(readonly code: McpTaskReceiptFailureCode) { super(messages[code]); }
}

function endpointURL(value: string | URL): URL {
  let endpoint: URL;
  try { endpoint = new URL(value); } catch { throw new McpTaskReceiptError('invalid_endpoint'); }
  if ((endpoint.protocol !== 'http:' && endpoint.protocol !== 'https:') || endpoint.username ||
      endpoint.password || endpoint.href.includes('#')) {
    throw new McpTaskReceiptError('invalid_endpoint');
  }
  return endpoint;
}

type Fetch = typeof globalThis.fetch;
type TransportOptions = { fetch?: Fetch };
type TransportConstructor = new (url: URL, options?: TransportOptions) => object;

/** Create an endpoint-bound official StreamableHTTPClientTransport for Gregale Tasks. */
export function createMcpTaskTransport<Transport extends TransportConstructor>(
  TransportClass: Transport, endpoint: string | URL, options?: ConstructorParameters<Transport>[1],
): InstanceType<Transport> {
  const href = endpointURL(endpoint).href;
  const binding: TransportBinding = { endpointSha256: createHash('sha256').update(href).digest('hex'), operations: new Map() };
  const fetcher = createGuardedMcpTaskSubmissionFetch(options?.fetch, async (operationId, params, metadata, headers) => {
    const guard = binding.operations.get(operationId);
    if (!guard) throw new McpTaskReceiptError('unbound_connection');
    await guard(operationId, params, metadata, headers);
  });
  const fetch: Fetch = async (input, init) => {
    const request = input instanceof Request ? input : undefined;
    const headers = new Headers(init?.headers ?? request?.headers);
    if (!receiptMethods.has(headers.get('mcp-method') ?? '')) return fetcher(input, init);
    const destination = endpointURL(input instanceof Request ? input.url : input).href;
    if (destination !== href) throw new McpTaskReceiptError('endpoint_mismatch');
    const response = await fetcher(input, { ...init, redirect: 'manual' });
    if ((response.status >= 300 && response.status < 400) ||
        (response.url && endpointURL(response.url).href !== href)) {
      void response.body?.cancel().catch(() => undefined);
      throw new McpTaskReceiptError('endpoint_mismatch');
    }
    return response;
  };
  const watch = taskWatchTransport(href, fetch);
  const transport = new TransportClass(new URL(href), { ...options, fetch: watch.fetch }) as InstanceType<Transport>;
  transports.set(transport, binding);
  watch.bind(transport);
  return transport;
}

/** Create an endpoint-bound official StreamableHTTPClientTransport for Task receipts. */
export const createMcpTaskReceiptTransport = createMcpTaskTransport;

function transportBinding(connection: McpTaskReceiptConnection): TransportBinding {
  const binding = connection.transport && transports.get(connection.transport);
  if (!binding) throw new McpTaskReceiptError('unbound_connection');
  return binding;
}

function checkEndpoint(connection: McpTaskReceiptConnection, expected: string): TransportBinding {
  const binding = transportBinding(connection);
  if (binding.endpointSha256 !== expected) throw new McpTaskReceiptError('endpoint_mismatch');
  return binding;
}

function newSubmissionKey(): string {
  const bytes = randomBytes(16);
  bytes.writeUIntBE(Date.now(), 0, 6);
  bytes[6] = (bytes[6]! & 0x0f) | 0x70;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = bytes.toString('hex');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function scope(options: McpTaskSubmissionOptions): Required<McpTaskSubmissionOptions> {
  const timeoutMs = options.timeoutMs ?? 30_000;
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 2 ** 31 - 1) {
    throw new RangeError('Invalid MCP Task receipt timeout');
  }
  const deadline = AbortSignal.timeout(timeoutMs);
  return { timeoutMs, signal: options.signal ? AbortSignal.any([options.signal, deadline]) : deadline };
}

async function receiptPath(path: string): Promise<string> {
  try {
    if (!path.trim()) throw new McpTaskReceiptError('invalid_receipt');
    const absolute = resolve(path);
    return join(await realpath(dirname(absolute)), basename(absolute));
  } catch (error) {
    if (error instanceof McpTaskReceiptError) throw error;
    throw new McpTaskReceiptError('receipt_io');
  }
}

function isDigest(value: unknown): value is string {
  return typeof value === 'string' && sha256Pattern.test(value);
}

function decodeReceipt(bytes: Uint8Array): Receipt {
  try {
    const receipt = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as Partial<RetryReceipt> |
      Partial<ReadOnlyReceipt> | null;
    if (receipt && isDigest(receipt.endpointSha256) && isSubmissionKey(receipt.submissionKey) &&
        (!Object.hasOwn(receipt, 'taskId') || isMcpTaskId(receipt.taskId))) {
      const keys = ['version', 'endpointSha256', 'submissionKey', 'taskId'];
      if (receipt.version === 1 && Object.keys(receipt).every(key => keys.includes(key))) return receipt as ReadOnlyReceipt;
      if (receipt.version === 2 && isDigest(receipt.requestSha256) && isDigest(receipt.credentialProof) &&
          Object.keys(receipt).every(key => keys.includes(key) || key === 'requestSha256' || key === 'credentialProof')) {
        return receipt as RetryReceipt;
      }
    }
  } catch {
    // Treat malformed text and malformed JSON as invalid receipts.
  }
  throw new McpTaskReceiptError('invalid_receipt');
}

function sameSubmission(saved: Receipt, receipt: Receipt): boolean {
  return saved.version === receipt.version && saved.endpointSha256 === receipt.endpointSha256 &&
    saved.submissionKey === receipt.submissionKey &&
    (saved.version !== 2 || (receipt.version === 2 && saved.requestSha256 === receipt.requestSha256 &&
      saved.credentialProof === receipt.credentialProof));
}

function hasFsCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}

async function loadReceipt(path: string): Promise<Receipt> {
  try {
    const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    try {
      const info = await file.stat();
      if (!info.isFile() || (info.mode & 0o077) !== 0 || info.size > maxReceiptBytes ||
          (process.geteuid && info.uid !== process.geteuid())) {
        throw new McpTaskReceiptError('invalid_receipt');
      }
      const bytes = Buffer.alloc(maxReceiptBytes + 1);
      let length = 0;
      while (length < bytes.length) {
        const { bytesRead } = await file.read(bytes, length, bytes.length - length, length);
        if (bytesRead === 0) break;
        length += bytesRead;
      }
      if (length > maxReceiptBytes) throw new McpTaskReceiptError('invalid_receipt');
      return decodeReceipt(bytes.subarray(0, length));
    } finally {
      await file.close();
    }
  } catch (error) {
    if (error instanceof McpTaskReceiptError) throw error;
    throw new McpTaskReceiptError(hasFsCode(error, 'ELOOP') ? 'invalid_receipt' : 'receipt_io');
  }
}

async function writeReceipt(path: string, receipt: Receipt): Promise<void> {
  const create = receipt.taskId === undefined;
  const parent = dirname(path);
  const temporary = join(parent, `.gregale-task-receipt-${randomUUID()}`);
  let createdTemporary = false;
  try {
    const directory = await open(parent, constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW);
    try {
      const { mode } = await directory.stat();
      if ((mode & 0o022) !== 0 && (mode & 0o1000) === 0) throw new McpTaskReceiptError('invalid_receipt');
      const file = await open(temporary, 'wx', 0o600);
      createdTemporary = true;
      try {
        await file.chmod(0o600);
        await file.writeFile(JSON.stringify(receipt) + '\n');
        await file.sync();
      } finally {
        await file.close();
      }
      if (create) {
        await link(temporary, path);
        await unlink(temporary);
        createdTemporary = false;
      } else {
        // Check the original receipt just before replacing it. Only the
        // creator adds the ID, and readers always see a complete file.
        const current = await loadReceipt(path);
        if (!sameSubmission(current, receipt)) throw new McpTaskReceiptError('invalid_receipt');
        if (current.taskId !== undefined) {
          if (current.taskId !== receipt.taskId) throw new McpTaskReceiptError('task_mismatch');
          await directory.sync();
          return;
        }
        await rename(temporary, path);
        createdTemporary = false;
      }
      await directory.sync();
    } finally {
      await directory.close();
    }
  } catch (error) {
    if (error instanceof McpTaskReceiptError) throw error;
    throw new McpTaskReceiptError(create && hasFsCode(error, 'EEXIST') ? 'receipt_exists' : 'receipt_io');
  } finally {
    if (createdTemporary) await unlink(temporary).catch(() => undefined);
  }
}

function submissionDigest(params: Record<string, unknown>, metadata: Record<string, unknown>): string {
  return jobValueHash({
    tool: params.name,
    arguments: params.arguments,
    capabilities: metadata['io.modelcontextprotocol/clientCapabilities'],
  });
}

function bearerCredential(headers: Headers): string | undefined {
  return /^Bearer[ \t]+([A-Za-z0-9._~+\/-]+=*)$/i.exec(headers.get('Authorization') ?? '')?.[1];
}

function credentialProof(receipt: Omit<RetryReceipt, 'credentialProof'>, credential: string): string {
  return createHmac('sha256', credential)
    .update('gregale/node-sdk/mcp-task-receipt/v2:')
    .update(receipt.endpointSha256).update(receipt.submissionKey).update(receipt.requestSha256)
    .digest('hex');
}

function verifyCredential(receipt: RetryReceipt, credential: string | undefined): void {
  if (!credential || !timingSafeEqual(Buffer.from(credentialProof(receipt, credential), 'hex'),
    Buffer.from(receipt.credentialProof, 'hex'))) {
    throw new McpTaskReceiptError('credential_mismatch');
  }
}

function requireRetryWindow(receipt: Receipt): void {
  const issued = Number.parseInt(receipt.submissionKey.slice(0, 8) + receipt.submissionKey.slice(9, 13), 16);
  const now = Date.now();
  if (now < issued || now - issued >= submissionWindowMs) throw new McpTaskReceiptError('retry_window_closed');
}

async function runReceiptSubmission(
  connection: McpTaskReceiptConnection, path: string, seed: Receipt, input: McpTaskReceiptSubmission,
  operation: ReturnType<typeof scope>, retry: boolean,
): Promise<McpTaskReceiptSubmissionOutcome> {
  operation.signal.throwIfAborted();
  const binding = checkEndpoint(connection, seed.endpointSha256);
  const request = prepareMcpTaskSubmission(connection, { ...input, submissionKey: seed.submissionKey });
  const requestSha256 = submissionDigest(request.params, request.params._meta);
  if (seed.version === 2 && seed.requestSha256 !== requestSha256) throw new McpTaskReceiptError('request_mismatch');

  let receipt = retry ? seed : undefined;
  let guardError: unknown;
  const operationId = randomUUID();
  request.params._meta[localReceiptMeta] = operationId;
  binding.operations.set(operationId, async (_, params, metadata, headers) => {
    try {
      operation.signal.throwIfAborted();
      if (metadata[submissionKeyMeta] !== seed.submissionKey || submissionDigest(params, metadata) !== requestSha256) {
        throw new McpTaskReceiptError('request_mismatch');
      }
      if (retry) requireRetryWindow(seed);
      const credential = bearerCredential(headers);
      if (!receipt) {
        // Capture the bearer on the prepared HTTP request before allowing it to leave.
        let initial = seed;
        if (credential) {
          const bound = { ...seed, version: 2 as const, requestSha256 };
          initial = { ...bound, credentialProof: credentialProof(bound, credential) };
        }
        await writeReceipt(path, initial);
        receipt = initial;
      }
      if (receipt.version === 2) verifyCredential(receipt, credential);
      else if (credential) throw new McpTaskReceiptError('credential_mismatch');
      operation.signal.throwIfAborted();
    } catch (error) {
      guardError = error;
      throw error;
    }
  });

  let result: McpTaskSubmissionOutcome;
  try {
    result = await sendMcpTaskSubmission(connection, request, operation);
  } catch (error) {
    throw guardError ?? error;
  } finally {
    binding.operations.delete(operationId);
  }
  if (guardError !== undefined) throw guardError;
  if (result.outcome === 'unconfirmed') return result;
  if (!receipt) throw new McpTaskReceiptError('unbound_connection');
  try {
    await writeReceipt(path, { ...receipt, taskId: result.task.taskId });
    return { ...result, taskIdRecorded: true };
  } catch (error) {
    if (error instanceof McpTaskReceiptError && error.code === 'task_mismatch') throw error;
    // Admission is confirmed even if the local acknowledgement could not be saved.
    return { ...result, taskIdRecorded: false };
  }
}

/** Publish a private receipt before admitting the keyed submission. */
export async function submitMcpTaskWithReceipt(
  connection: McpTaskReceiptConnection, path: string, submission: McpTaskReceiptSubmission,
  options: McpTaskSubmissionOptions = {},
): Promise<McpTaskReceiptSubmissionOutcome> {
  const operation = scope(options);
  operation.signal.throwIfAborted();
  const { endpointSha256 } = transportBinding(connection);
  const file = await receiptPath(path);
  const seed: ReadOnlyReceipt = { version: 1, endpointSha256, submissionKey: newSubmissionKey() };
  return runReceiptSubmission(connection, file, seed, submission, operation, false);
}

/** Explicitly retry the original keyed submission within the receipt's admission window. */
export async function retryMcpTaskFromReceipt(
  connection: McpTaskReceiptConnection, path: string, submission: McpTaskReceiptSubmission,
  options: McpTaskSubmissionOptions = {},
): Promise<McpTaskReceiptSubmissionOutcome> {
  const operation = scope(options);
  operation.signal.throwIfAborted();
  const file = await receiptPath(path);
  const receipt = await loadReceipt(file);
  checkEndpoint(connection, receipt.endpointSha256);
  if (receipt.version !== 2) throw new McpTaskReceiptError('retry_unavailable');
  requireRetryWindow(receipt);
  return runReceiptSubmission(connection, file, receipt, submission, operation, true);
}

/** Recover a retained Task with the current credential using read-only requests. */
export async function resumeMcpTaskFromReceipt(
  connection: McpTaskReceiptConnection, path: string, options: McpTaskSubmissionOptions = {},
): Promise<McpTaskSnapshot> {
  const operation = scope(options);
  operation.signal.throwIfAborted();
  const { endpointSha256: selectedEndpoint } = transportBinding(connection);
  const receipt = await loadReceipt(await receiptPath(path));
  if (receipt.endpointSha256 !== selectedEndpoint) throw new McpTaskReceiptError('endpoint_mismatch');
  checkEndpoint(connection, selectedEndpoint);
  operation.signal.throwIfAborted();
  const taskId = receipt.taskId ??
    (await lookupMcpTaskBySubmissionKey(connection, receipt.submissionKey, operation)).taskId;
  checkEndpoint(connection, selectedEndpoint);
  return getMcpTask(connection, taskId, operation);
}
