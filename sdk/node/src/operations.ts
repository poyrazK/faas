// ADR-586: customer PostgreSQL business writes and replayable handler responses.
import { createHash } from "node:crypto";
import { operationReceiptTransaction, type OperationPool, type OperationTransaction, type OperationTransactionResult } from './operation-receipt.js';
export { OperationConflictError, OperationCommitUnknownError, type OperationPool, type OperationTransaction, type OperationConnection, type OperationTransactionResult } from './operation-receipt.js';
import type { ManagedOperationEffect } from "./generated/models/ManagedOperationEffect.js";
import {
  OPERATION_REQUEST_BYTES, OPERATION_IDENTITY_BYTES, OPERATION_RESPONSE_BYTES,
  OPERATION_EFFECTS, OPERATION_PAYLOAD_BYTES, OPERATION_TYPE_BYTES,
} from "./operation-contract.js";

const supportedOperation: unique symbol = Symbol("Gregale managed operation support");

export interface OperationRequest {
  readonly [supportedOperation]: true;
  operationId: string;
  accountId: string;
  appId: string;
  platformTenantId?: string;
  generation: string;
  method: string;
  path: string;
  body: Uint8Array;
}

export interface OperationOutcome {
  result: unknown;
  effects?: readonly ManagedOperationEffect[];
}

function uuid(value: string): string {
  if (typeof value !== "string" || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)
      || /^0{8}-0{4}-0{4}-0{4}-0{12}$/.test(value)) throw new TypeError("operation identity must be a nonzero UUID");
  return value.toLowerCase();
}

function normalize(request: OperationRequest): OperationRequest {
  if (request[supportedOperation] !== true) throw new TypeError("use operationRequestFromHeaders with negotiated support");
  if (typeof request.generation !== "string" || !/^[1-9][0-9]{0,18}$/.test(request.generation)
      || BigInt(request.generation) > 9223372036854775807n) throw new TypeError("operation generation must be a positive int64");
  if (typeof request.method !== "string" || !/^[A-Z]+$/.test(request.method) || request.method.length > OPERATION_IDENTITY_BYTES
      || typeof request.path !== "string" || !request.path.startsWith("/") || /[\r\n\0]/.test(request.path)
      || Buffer.from(request.path).toString("utf8") !== request.path || !(request.body instanceof Uint8Array)
      || Buffer.byteLength(request.method) + Buffer.byteLength(request.path) + request.body.byteLength > OPERATION_REQUEST_BYTES) {
    throw new TypeError("invalid or oversized operation request");
  }
  return Object.freeze({ ...request, operationId: uuid(request.operationId), accountId: uuid(request.accountId),
    appId: uuid(request.appId), platformTenantId: request.platformTenantId ? uuid(request.platformTenantId) : undefined,
    body: Buffer.from(request.body) });
}

/** Use only behind Gregale ingress, which strips and authors these headers. */
export function operationRequestFromHeaders(
  headers: Record<string, string | readonly string[] | undefined>, method: string, path: string, body: Uint8Array,
): OperationRequest {
  const read = (name: string, optional = false): string => {
    const values = Object.entries(headers).filter(([key]) => key.toLowerCase() === name)
      .flatMap(([, value]) => value === undefined ? [] : typeof value === "string" ? [value] : [...value]);
    if (values.length === 0 && optional) return "";
    if (values.length !== 1 || !values[0]) throw new TypeError(`operation requires one ${name} header`);
    return values[0];
  };
  if (read("x-gregale-operation-result-version") !== "1") throw new TypeError("managed operation results are not supported");
  return normalize({ [supportedOperation]: true, operationId: read("x-gregale-operation-id"), accountId: read("x-faas-tenant-id"),
    appId: read("x-faas-app-id"), platformTenantId: read("x-faas-platform-tenant-id", true),
    generation: read("x-gregale-operation-generation"), method, path, body });
}

export function operationRequestDigest(input: OperationRequest): Uint8Array {
  const request = normalize(input);
  return createHash("sha256").update("gregale-operation-request-v1\n")
    .update(request.method).update("\n").update(request.path).update("\n").update(request.body).digest();
}

function encode(outcome: OperationOutcome): string {
  if (outcome === null || typeof outcome !== "object" || Object.keys(outcome).some(key => !["result", "effects"].includes(key))
      || !("result" in outcome)) throw new TypeError("operation outcome requires result and optional effects");
  const body = JSON.stringify({ gregale_operation_result: 1, result: outcome.result, effects: outcome.effects === undefined ? [] : outcome.effects }, (_key, value: unknown) => {
    if (value === undefined || typeof value === "function" || typeof value === "symbol" || typeof value === "bigint"
        || (typeof value === "number" && !Number.isFinite(value))) throw new TypeError("operation outcome must contain JSON values");
    return value;
  });
  validateBody(body);
  return body;
}

// The input has already passed JSON.parse. Locate original value spans rather
// than re-encode parsed numbers, which can lose precision or change wire size.
function valueEnd(source: string, start: number): number {
  if (source[start] === '"') {
    for (let index = start + 1; index < source.length; index++) {
      if (source[index] === "\\") index++;
      else if (source[index] === '"') return index + 1;
    }
  }
  if (source[start] === "{" || source[start] === "[") {
    let depth = 0;
    for (let index = start; index < source.length; index++) {
      const char = source[index];
      if (char === '"') index = valueEnd(source, index) - 1;
      else if (char === "{" || char === "[") depth++;
      else if ((char === "}" || char === "]") && --depth === 0) return index + 1;
    }
  }
  let end = start;
  while (end < source.length && !/[\s,}\]]/.test(source[end]!)) end++;
  return end;
}

function rawValues(source: string): Array<[string, string]> {
  const object = source.trimStart()[0] === "{";
  let index = source.search(/[\[{]/) + 1;
  const values: Array<[string, string]> = [];
  const space = () => { while (/\s/.test(source[index] ?? "")) index++; };
  while (index < source.length) {
    space();
    if (source[index] === "}" || source[index] === "]") break;
    let key = "";
    if (object) {
      const end = valueEnd(source, index);
      key = JSON.parse(source.slice(index, end)) as string;
      index = end;
      space();
      index++; // colon
      space();
    }
    const end = valueEnd(source, index);
    values.push([key, source.slice(index, end)]);
    index = end;
    space();
    if (source[index] === ",") index++;
  }
  return values;
}

function validateBody(body: string): void {
  if (Buffer.byteLength(body) > OPERATION_RESPONSE_BYTES) throw new TypeError("operation response exceeds platform limit");
  const value = JSON.parse(body) as Record<string, unknown>;
  if (!value || Array.isArray(value) || Object.keys(value).some(key => !["gregale_operation_result", "result", "effects"].includes(key))
      || value.gregale_operation_result !== 1 || !("result" in value) || !Array.isArray(value.effects) || value.effects.length > OPERATION_EFFECTS) {
    throw new TypeError("invalid saved operation response");
  }
  const names = new Set<string>();
  const rawEffects = rawValues(new Map(rawValues(body)).get("effects")!);
  for (const [index, effect] of (value.effects as ManagedOperationEffect[]).entries()) {
    if (!effect || typeof effect !== "object" || Object.keys(effect).some(key => !["name", "webhook_id", "type", "payload"].includes(key))
        || typeof effect.name !== "string" || !/^[a-z][a-z0-9-]{0,62}$/.test(effect.name) || names.has(effect.name)
        || typeof effect.type !== "string" || !/^[a-z][a-z0-9_.-]*$/.test(effect.type) || effect.type.length > OPERATION_TYPE_BYTES
        || !("payload" in effect)) {
      throw new TypeError("invalid operation effect");
    }
    const payload = new Map(rawValues(rawEffects[index]![1])).get("payload");
    if (payload === undefined || Buffer.byteLength(payload) > OPERATION_PAYLOAD_BYTES) throw new TypeError("operation payload exceeds platform limit");
    uuid(effect.webhook_id);
    names.add(effect.name);
  }
}

/** Owns a fresh READ COMMITTED transaction. Callback must not commit or make external side effects. */
export async function withOperationTransaction(
  pool: OperationPool, input: OperationRequest,
  handler: (transaction: OperationTransaction) => Promise<OperationOutcome>,
): Promise<OperationTransactionResult> {
  const request = normalize(input);
  const digest = Buffer.from(operationRequestDigest(request));
  return operationReceiptTransaction(pool, request, digest, 'managed', async tx => encode(await handler(tx)), validateBody);
}
