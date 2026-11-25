import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { link, open, realpath, rename, unlink } from 'node:fs/promises';
import { basename, dirname, join, resolve } from 'node:path';
import { isMcpTaskId } from './mcp-task-inbox.js';
import { getMcpTask, type McpTaskConnection, type McpTaskSnapshot } from './mcp-task-result.js';
import {
  createMcpTaskSubmissionFetch, isSubmissionKey, lookupMcpTaskBySubmissionKey,
  prepareMcpTaskSubmission, sendMcpTaskSubmission,
  type McpTaskSubmission, type McpTaskSubmissionConnection, type McpTaskSubmissionOptions, type McpTaskSubmissionOutcome,
} from './mcp-task-submission.js';

const maxReceiptBytes = 4096;
const transports = new WeakMap<object, string>();
const receiptMethods = new Set(['gregale/tasks/submit', 'gregale/tasks/lookup', 'tasks/get']);
const sha256Pattern = /^[0-9a-f]{64}(?![\s\S])/u;

export type McpTaskReceiptConnection = McpTaskConnection & McpTaskSubmissionConnection & {
  readonly transport: object | undefined;
};
export type McpTaskReceiptSubmission = Omit<McpTaskSubmission, 'submissionKey'>;
export type McpTaskReceiptSubmissionOutcome =
  | (Extract<McpTaskSubmissionOutcome, { outcome: 'accepted' }> & { readonly taskIdRecorded: boolean })
  | Extract<McpTaskSubmissionOutcome, { outcome: 'unconfirmed' }>;

type Receipt = {
  version: 1;
  endpointSha256: string;
  submissionKey: string;
  taskId?: string;
};

const messages = {
  invalid_endpoint: 'MCP Task receipts require an HTTP or HTTPS endpoint without credentials or a fragment',
  unbound_connection: 'Connect using createMcpTaskReceiptTransport before using a Task receipt',
  endpoint_mismatch: 'MCP Task receipt does not match the selected endpoint',
  invalid_receipt: 'Invalid or non-private MCP Task receipt',
  receipt_exists: 'MCP Task receipt already exists. Resume it to check the saved submission',
  receipt_io: 'Could not read or write the MCP Task receipt',
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

/** Create an endpoint-bound official StreamableHTTPClientTransport for Task receipts. */
export function createMcpTaskReceiptTransport<Transport extends TransportConstructor>(
  TransportClass: Transport, endpoint: string | URL, options?: ConstructorParameters<Transport>[1],
): InstanceType<Transport> {
  const href = endpointURL(endpoint).href;
  const fetcher = createMcpTaskSubmissionFetch(options?.fetch);
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
  const transport = new TransportClass(new URL(href), { ...options, fetch }) as InstanceType<Transport>;
  transports.set(transport, createHash('sha256').update(href).digest('hex'));
  return transport;
}

function fingerprint(connection: McpTaskReceiptConnection): string {
  const binding = connection.transport && transports.get(connection.transport);
  if (!binding) throw new McpTaskReceiptError('unbound_connection');
  return binding;
}

function checkEndpoint(connection: McpTaskReceiptConnection, expected: string): void {
  if (fingerprint(connection) !== expected) throw new McpTaskReceiptError('endpoint_mismatch');
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

function decodeReceipt(bytes: Uint8Array): Receipt {
  try {
    const receipt = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as Partial<Receipt> | null;
    if (receipt?.version === 1 && typeof receipt.endpointSha256 === 'string' &&
        sha256Pattern.test(receipt.endpointSha256) && isSubmissionKey(receipt.submissionKey) &&
        (!Object.hasOwn(receipt, 'taskId') || isMcpTaskId(receipt.taskId)) &&
        Object.keys(receipt).every(key => ['version', 'endpointSha256', 'submissionKey', 'taskId'].includes(key))) {
      return receipt as Receipt;
    }
  } catch {
    // Treat malformed text and malformed JSON as invalid receipts.
  }
  throw new McpTaskReceiptError('invalid_receipt');
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
        if (current.endpointSha256 !== receipt.endpointSha256 || current.submissionKey !== receipt.submissionKey ||
            current.taskId !== undefined) throw new McpTaskReceiptError('invalid_receipt');
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

/** Publish a private receipt before admitting the keyed submission. */
export async function submitMcpTaskWithReceipt(
  connection: McpTaskReceiptConnection, path: string, submission: McpTaskReceiptSubmission,
  options: McpTaskSubmissionOptions = {},
): Promise<McpTaskReceiptSubmissionOutcome> {
  const operation = scope(options);
  operation.signal.throwIfAborted();
  const endpointSha256 = fingerprint(connection);
  const file = await receiptPath(path);
  const seed: Receipt = { version: 1, endpointSha256, submissionKey: newSubmissionKey() };
  const request = prepareMcpTaskSubmission(connection, { ...submission, submissionKey: seed.submissionKey });
  operation.signal.throwIfAborted();
  await writeReceipt(file, seed);
  operation.signal.throwIfAborted();
  checkEndpoint(connection, endpointSha256);
  const result = await sendMcpTaskSubmission(connection, request, operation);
  if (result.outcome === 'unconfirmed') return result;

  try {
    await writeReceipt(file, { ...seed, taskId: result.task.taskId });
    return { ...result, taskIdRecorded: true };
  } catch {
    // Admission is confirmed even if the local acknowledgement could not be saved.
    return { ...result, taskIdRecorded: false };
  }
}

/** Recover a retained Task with the current credential using read-only requests. */
export async function resumeMcpTaskFromReceipt(
  connection: McpTaskReceiptConnection, path: string, options: McpTaskSubmissionOptions = {},
): Promise<McpTaskSnapshot> {
  const operation = scope(options);
  operation.signal.throwIfAborted();
  const selectedEndpoint = fingerprint(connection);
  const receipt = await loadReceipt(await receiptPath(path));
  if (receipt.endpointSha256 !== selectedEndpoint) throw new McpTaskReceiptError('endpoint_mismatch');
  checkEndpoint(connection, selectedEndpoint);
  operation.signal.throwIfAborted();
  const taskId = receipt.taskId ??
    (await lookupMcpTaskBySubmissionKey(connection, receipt.submissionKey, operation)).taskId;
  checkEndpoint(connection, selectedEndpoint);
  return getMcpTask(connection, taskId, operation);
}
