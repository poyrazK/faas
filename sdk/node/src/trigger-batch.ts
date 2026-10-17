// Batched trigger delivery helpers (ADR-1321).
//
// A trigger with `delivery: batch` invokes the function once with every
// claimed record, with header X-Gregale-Batch-Size and body:
//
//   {"records": [{"item_identifier": "…", "data": {…}, "headers": {…}, "metadata": {…}},
//                {"item_identifier": "…", "data_base64": "…"}]}
//
// `data` carries a payload that was valid JSON and `data_base64` any other
// payload. The function answers 2xx to complete every record, or
// {"batchItemFailures": [{"itemIdentifier": "…"}]} to retry some of them.
// handleTriggerBatch runs a handler for each record and builds that answer.

/** Header present on every batch delivery; its value is the record count. */
export const TRIGGER_BATCH_SIZE_HEADER = 'x-gregale-batch-size';

const MAX_CONCURRENCY = 100;

export interface TriggerBatchRecord<T = unknown> {
  readonly itemIdentifier: string;
  /** True when the payload was JSON; `data` then holds it, possibly null. */
  readonly isJSON: boolean;
  /** The payload parsed as JSON, or undefined when it was not JSON. */
  readonly data: T | undefined;
  /**
   * The payload bytes. A JSON payload arrives parsed, so these are its
   * re-encoding and may differ in spacing from what was published.
   */
  readonly bytes: Uint8Array;
  readonly headers: Readonly<Record<string, string>>;
  /** Source details, such as a realtime channel and sequence or a Kafka offset. */
  readonly metadata: Readonly<Record<string, unknown>>;
  /** The payload as UTF-8 text. */
  text(): string;
}

export interface TriggerBatchResponse {
  batchItemFailures: { itemIdentifier: string }[];
}

/** The body is not a batch delivery envelope. */
export class TriggerBatchError extends Error {
  override name = 'TriggerBatchError';
}

export interface HandleTriggerBatchOptions<T = unknown> {
  /**
   * Records handled at once, 1-100 (default 1: one after another in delivery
   * order). Use more only when records do not depend on each other's order.
   */
  concurrency?: number;
  /**
   * After a record fails, report the remaining records for retry without
   * running them, so they are retried in order. Each retry counts as an
   * attempt for those records too. Needs concurrency 1. Default false: every
   * record runs, and only the failures are retried.
   */
  stopOnFailure?: boolean;
  /** Receives each record's error before the record is reported for retry. */
  onError?(error: unknown, record: TriggerBatchRecord<T>): void;
}

type HeaderSource = Headers | Readonly<Record<string, string | readonly string[] | undefined>>;

/** Reports whether a request is a batch delivery (ADR-1321). */
export function isTriggerBatch(headers: HeaderSource): boolean {
  if (typeof (headers as Headers).get === 'function') {
    return (headers as Headers).get(TRIGGER_BATCH_SIZE_HEADER) !== null;
  }
  return Object.keys(headers).some((name) => name.toLowerCase() === TRIGGER_BATCH_SIZE_HEADER);
}

/**
 * Validates a batch delivery body and returns its records in delivery order.
 * The body may be the parsed JSON object, its text, or its bytes.
 */
export function parseTriggerBatch<T = unknown>(body: unknown): TriggerBatchRecord<T>[] {
  const envelope = decodeBody(body);
  if (!isObject(envelope) || !Array.isArray(envelope.records)) {
    throw new TriggerBatchError('trigger batch body must be an object with a records array');
  }
  const seen = new Set<string>();
  return envelope.records.map((raw, index) => {
    const record = parseRecord<T>(raw, index);
    if (seen.has(record.itemIdentifier)) {
      throw new TriggerBatchError(`trigger batch record ${index} repeats item_identifier ${record.itemIdentifier}`);
    }
    seen.add(record.itemIdentifier);
    return record;
  });
}

/**
 * Runs `handler` for every record of a batch delivery and returns the
 * response body for the function to send with status 200. A record whose
 * handler throws or rejects is listed in `batchItemFailures` and retried; the
 * others are complete. A malformed body throws TriggerBatchError, which should
 * fail the request so the whole batch is retried.
 */
export async function handleTriggerBatch<T = unknown>(
  body: unknown,
  handler: (record: TriggerBatchRecord<T>) => unknown,
  options: HandleTriggerBatchOptions<T> = {},
): Promise<TriggerBatchResponse> {
  const concurrency = options.concurrency ?? 1;
  if (!Number.isInteger(concurrency) || concurrency < 1 || concurrency > MAX_CONCURRENCY) {
    throw new TypeError(`concurrency must be an integer from 1 to ${MAX_CONCURRENCY}`);
  }
  if (options.stopOnFailure && concurrency !== 1) {
    throw new TypeError('stopOnFailure needs concurrency 1');
  }
  const records = parseTriggerBatch<T>(body);
  const failed = new Array<boolean>(records.length).fill(false);
  const run = async (index: number): Promise<boolean> => {
    const record = records[index]!;
    try {
      await handler(record);
      return true;
    } catch (error) {
      try { options.onError?.(error, record); } catch { /* reporting must not change the outcome */ }
      failed[index] = true;
      return false;
    }
  };
  if (concurrency === 1) {
    for (let index = 0; index < records.length; index++) {
      if (!(await run(index)) && options.stopOnFailure) {
        failed.fill(true, index);
        break;
      }
    }
  } else {
    let next = 0;
    const worker = async () => {
      while (next < records.length) await run(next++);
    };
    await Promise.all(Array.from({ length: Math.min(concurrency, records.length) }, worker));
  }
  return {
    batchItemFailures: records.filter((_, index) => failed[index]).map((record) => ({ itemIdentifier: record.itemIdentifier })),
  };
}

function decodeBody(body: unknown): unknown {
  if (typeof body !== 'string' && !(body instanceof Uint8Array)) return body;
  const text = typeof body === 'string' ? body : new TextDecoder().decode(body);
  try {
    return JSON.parse(text);
  } catch {
    throw new TriggerBatchError('trigger batch body is not JSON');
  }
}

function parseRecord<T>(raw: unknown, index: number): TriggerBatchRecord<T> {
  if (!isObject(raw) || typeof raw.item_identifier !== 'string' || raw.item_identifier === '') {
    throw new TriggerBatchError(`trigger batch record ${index} needs an item_identifier`);
  }
  const hasData = 'data' in raw, hasBase64 = 'data_base64' in raw;
  if (hasData && hasBase64) {
    throw new TriggerBatchError(`trigger batch record ${index} has both data and data_base64`);
  }
  let bytes: Uint8Array;
  if (hasData) {
    bytes = new TextEncoder().encode(JSON.stringify(raw.data));
  } else if (hasBase64) {
    if (typeof raw.data_base64 !== 'string') {
      throw new TriggerBatchError(`trigger batch record ${index} data_base64 must be a string`);
    }
    bytes = decodeBase64(raw.data_base64, index);
  } else {
    bytes = new Uint8Array();
  }
  const headers = isObject(raw.headers) ? stringRecord(raw.headers) : {};
  const metadata = isObject(raw.metadata) ? { ...raw.metadata } : {};
  const text = () => new TextDecoder().decode(bytes);
  return Object.freeze({
    itemIdentifier: raw.item_identifier,
    isJSON: hasData,
    data: hasData ? (raw.data as T) : undefined,
    bytes, headers: Object.freeze(headers), metadata: Object.freeze(metadata), text,
  });
}

function decodeBase64(value: string, index: number): Uint8Array {
  let binary: string;
  try {
    binary = atob(value);
  } catch {
    throw new TriggerBatchError(`trigger batch record ${index} has invalid data_base64`);
  }
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}

function stringRecord(value: Record<string, unknown>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, entry] of Object.entries(value)) {
    if (typeof entry === 'string') out[key] = entry;
  }
  return out;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
