import {createHash} from 'node:crypto';
import {operationRequestDigest, operationTransactionWithGuard, type OperationRequest, type OperationOutcome} from './operations.js';
import {customerOperationRequestDigest, customerOperationTransactionWithGuard, type CustomerOperationTransactionRequest} from './customer-operation-transactions.js';
import type {CustomerOperationTransaction} from './customer-operation-milestones.js';
import type {OperationMilestoneReport} from './customer-operations.js';
import type {OperationWorkflowStateReport} from './customer-operation-workflow-states.js';
import type {OperationPool, OperationTransaction, OperationReceiptGuard, OperationTransactionResult} from './operation-receipt.js';
import type {DataRepairCapture} from './data-repair.js';
import {captureReadinessProbe as limits} from './data-repair-readiness-probe.js';

export class DataRepairCaptureError extends Error {
  readonly code: 'repair_capture_missing' | 'repair_capture_mismatch' | 'repair_capture_incomplete' | 'repair_capture_unavailable';
  constructor(state: unknown) {
    const code = state === 'GR001' ? 'repair_capture_missing' : state === 'GR002' ? 'repair_capture_mismatch' : state === 'GR003' || state === '22023' ? 'repair_capture_incomplete' : 'repair_capture_unavailable';
    super(code === 'repair_capture_unavailable' ? `${code}: check the owner upgrade and writer grants` : code);
    this.name = 'DataRepairCaptureError'; this.code = code;
  }
}

function capture(request: OperationRequest | CustomerOperationTransactionRequest, input: DataRepairCapture, protocol: 'managed' | 'customer', digest: Uint8Array) {
  const config = {table: input.table, environment: input.environment, release: input.release};
  if (![[config.table, limits.max_table_bytes], [config.environment, limits.max_context_bytes], [config.release, limits.max_context_bytes]].every(([value, limit]) =>
    typeof value === 'string' && value.length > 0 && Buffer.byteLength(value) <= Number(limit) && !value.includes('\0') && Buffer.from(value).toString() === value) ||
    !config.table.includes('.') || config.table.trim() !== config.table) throw new TypeError('invalid repairable operation capture configuration');
  const scope = JSON.stringify({account_id: request.accountId.toLowerCase(), app_id: request.appId.toLowerCase(), environment: config.environment,
    ...(request.platformTenantId ? {platform_tenant_id: request.platformTenantId.toLowerCase()} : {})});
  const parameters = [request.operationId.toLowerCase(), scope, config.release, config.table, protocol, Buffer.from(digest)];
  const query = async (tx: OperationTransaction, sql: string, values: unknown[]) => {
    try { await tx.query(sql, values); } catch (error) { throw new DataRepairCaptureError((error as {code?: unknown})?.code); }
  };
  const phase = (tx: OperationTransaction, mode: string, body?: string) => query(tx,
    'SELECT gregale_repair.capture_transaction($1::uuid,$2::jsonb,$3,$4,$5,$6::bytea,$7::bytea,$8)',
    [...parameters, body === undefined ? null : createHash('sha256').update(body, 'utf8').digest(), mode]);
  const run = async <T>(tx: OperationTransaction, handler: () => Promise<T>) => {
    await phase(tx, 'begin'); const result = await handler();
    await query(tx, 'SELECT gregale_repair.finish_operation($1::uuid)', [parameters[0]]); return result;
  };
  const guard: OperationReceiptGuard = (tx, body, replayed) => phase(tx, replayed ? 'replay' : 'seal', body);
  return {run, guard};
}

/** Own capture, business writes and the managed receipt atomically. Replays
 * require the original capture configuration and never run the handler. */
export async function withRepairableOperationTransaction(
  pool: OperationPool, input: OperationRequest, config: DataRepairCapture,
  handler: (transaction: OperationTransaction) => Promise<OperationOutcome>,
): Promise<OperationTransactionResult> {
  const request = {...input, body: Buffer.from(input.body)};
  const contract = capture(request, config, 'managed', operationRequestDigest(request));
  return operationTransactionWithGuard(pool, request, tx => contract.run(tx, () => handler(tx)), contract.guard);
}

/** Customer Operations equivalent, retaining existing milestone/workflow guards. */
export async function withRepairableCustomerOperationTransaction(
  pool: OperationPool, input: CustomerOperationTransactionRequest, config: DataRepairCapture,
  handler: (transaction: CustomerOperationTransaction) => Promise<unknown>,
  validateMilestones?: (reports: OperationMilestoneReport[]) => Promise<unknown>,
  validateWorkflowStates?: (reports: OperationWorkflowStateReport[], milestones: OperationMilestoneReport[]) => Promise<unknown>,
): Promise<OperationTransactionResult> {
  const request = {...input, body: Buffer.from(input.body)};
  const contract = capture(request, config, 'customer', customerOperationRequestDigest(request));
  return customerOperationTransactionWithGuard(pool, request, tx => contract.run(tx, () => handler(tx)), contract.guard, validateMilestones, validateWorkflowStates);
}
