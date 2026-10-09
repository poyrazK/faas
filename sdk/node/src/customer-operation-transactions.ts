// ADR-713: result-only receipts for customer Operation HTTP executions.
import { createHash } from 'node:crypto';
import { OPERATION_REQUEST_BYTES, OPERATION_IDENTITY_BYTES, OPERATION_RESPONSE_BYTES, OPERATION_WORKFLOW_STATE_BATCH_BYTES } from './operation-contract.js';
import { operationHeaders, operationExecutionContext, type OperationRequestHeaders } from './operation-execution-context.js';
import { operationReceiptTransaction, type OperationPool, type OperationTransactionResult } from './operation-receipt.js';

import { milestoneTransaction, saveCustomerMilestones, type CustomerOperationTransaction } from './customer-operation-milestones.js';
import { saveCustomerWorkflowStates, type OperationWorkflowStateReport } from './customer-operation-workflow-states.js';
import type { OperationMilestoneReport } from './customer-operations.js';

const customerTransaction: unique symbol = Symbol('Gregale customer Operation transaction support');
export interface CustomerOperationHTTPRequest {
  headers: OperationRequestHeaders;
  method: string;
  /** Exact HTTP request target; do not parse and reconstruct it. */
  path: string;
  /** Capture the original body before middleware parses JSON. */
  body: Uint8Array;
}
export interface CustomerOperationTransactionRequest {
  readonly [customerTransaction]: true;
  operationId: string;
  accountId: string;
  appId: string;
  platformTenantId: string;
  resultMaxBytes: number;
  milestonesSupported: boolean;
  method: string;
  path: string;
  body: Uint8Array;
}

function uuid(value: string): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)
      || value === '00000000-0000-0000-0000-000000000000') throw new TypeError('customer Operation owner must be a nonzero UUID');
  return value.toLowerCase();
}

function normalize(request: CustomerOperationTransactionRequest): CustomerOperationTransactionRequest {
  if (request[customerTransaction] !== true) throw new TypeError('use customerOperationRequestFromHeaders with negotiated support');
  if (typeof request.milestonesSupported !== 'boolean' || !Number.isSafeInteger(request.resultMaxBytes) || request.resultMaxBytes <= 0 || request.resultMaxBytes > OPERATION_RESPONSE_BYTES
      || typeof request.method !== 'string' || !/^[A-Z]+$/.test(request.method) || request.method.length > OPERATION_IDENTITY_BYTES
      || typeof request.path !== 'string' || !request.path.startsWith('/') || /[\r\n\0]/.test(request.path)
      || Buffer.from(request.path).toString('utf8') !== request.path || !(request.body instanceof Uint8Array)
      || Buffer.byteLength(request.method) + Buffer.byteLength(request.path) + request.body.byteLength > OPERATION_REQUEST_BYTES) {
    throw new TypeError('invalid or oversized customer Operation transaction request');
  }
  return Object.freeze({ ...request, operationId: uuid(request.operationId), accountId: uuid(request.accountId),
    appId: uuid(request.appId), platformTenantId: uuid(request.platformTenantId), body: Buffer.from(request.body) });
}

/** Only for Gregale's trusted guest listener. This factory is not authentication. */
export function customerOperationRequestFromHeaders(
  input: OperationRequestHeaders, method: string, path: string, body: Uint8Array,
): CustomerOperationTransactionRequest {
  const headers = operationHeaders(input);
  if (headers.get('X-Gregale-Customer-Operation-Transaction-Version') !== '1') throw new TypeError('customer Operation transactions are not supported');
  const milestoneVersion = headers.get('X-Gregale-Customer-Operation-Milestone-Version');
  if (milestoneVersion !== null && milestoneVersion !== '1') throw new TypeError('unsupported Customer Operation milestone version');
  const execution = operationExecutionContext(headers, headers.get('X-Gregale-Customer-Operation-Id') ?? '');
  const maximum = headers.get('X-Gregale-Customer-Operation-Result-Max-Bytes') ?? '';
  if (!/^[1-9][0-9]*$/.test(maximum)) throw new TypeError('customer Operation requires a result byte limit');
  return normalize({ [customerTransaction]: true, operationId: execution.id,
    accountId: headers.get('X-Faas-Tenant-Id') ?? '', appId: headers.get('X-Faas-App-Id') ?? '',
    platformTenantId: headers.get('X-Faas-Platform-Tenant-Id') ?? '', milestonesSupported: headers.get('X-Gregale-Customer-Operation-Milestone-Version') === '1', resultMaxBytes: Number(maximum), method, path, body });
}

export function customerOperationRequestDigest(input: CustomerOperationTransactionRequest): Uint8Array {
  const request = normalize(input);
  return createHash('sha256').update('gregale-customer-operation-request-v1\n')
    .update(request.method).update('\n').update(request.path).update('\n').update(request.body).digest();
}

function validate(body: string, maximum: number): void {
  if (Buffer.byteLength(body) === 0 || Buffer.byteLength(body) > maximum) throw new TypeError('customer Operation result exceeds its byte limit');
  JSON.parse(body);
}

export async function withCustomerOperationTransaction(
  pool: OperationPool, input: CustomerOperationTransactionRequest,
  handler: (transaction: CustomerOperationTransaction) => Promise<unknown>,
  validateMilestones?: (reports: OperationMilestoneReport[]) => Promise<unknown>,
  validateWorkflowStates?: (reports: OperationWorkflowStateReport[], milestones: OperationMilestoneReport[]) => Promise<unknown>,
): Promise<OperationTransactionResult> {
  const request = normalize(input);
  const digest = Buffer.from(customerOperationRequestDigest(request));
  return operationReceiptTransaction(pool, request, digest, 'customer', async tx => {
    const reports: OperationMilestoneReport[] = [];
    const workflowStates: OperationWorkflowStateReport[] = [];
    if (request.milestonesSupported) await tx.query("SELECT 1 FROM public.gregale_customer_operation_milestones LIMIT 0");
    let open = true;
    let pendingGuards = 0;
    let guardFailed = false;
    let guardError: unknown;
    let result: unknown;
    const transaction = milestoneTransaction(tx, request, reports, workflowStates, () => open, error => { guardFailed = true; guardError = error; });
    const guard = transaction.guardedWorkflowTransition;
    transaction.guardedWorkflowTransition = async (...args) => {
      pendingGuards++;
      try { return await guard(...args); }
      finally { pendingGuards--; }
    };
    const reconcile=transaction.reconcileWorkflowState;
    transaction.reconcileWorkflowState=async (...args)=>{
      pendingGuards++;
      try {return await reconcile(...args);} finally {pendingGuards--;}
    };
    try { result = await handler(transaction); }
    finally { open = false; }
    if (pendingGuards) throw new TypeError("Readiness guards must be awaited before the callback returns");
    if (guardFailed) throw guardError;
    const body = JSON.stringify(result, (_key, value: unknown) => {
      if (value === undefined || typeof value === 'function' || typeof value === 'symbol' || typeof value === 'bigint'
          || (typeof value === 'number' && !Number.isFinite(value))) throw new TypeError('customer Operation result must contain JSON values');
      return value;
    });
    validate(body, request.resultMaxBytes);
    if (reports.length > 0) {
      if (!validateMilestones) throw new TypeError("Milestone schema validation is required before commit");
      await validateMilestones(reports);
      await saveCustomerMilestones(tx, request.operationId, reports);
    }
    if (workflowStates.length > 0) {
      if (!validateWorkflowStates) throw new TypeError('Workflow state validation is required before commit');
      const saved = await saveCustomerWorkflowStates(tx, request, workflowStates, reports);
      if (Buffer.byteLength(JSON.stringify({workflow_states: saved, milestones: reports})) > OPERATION_WORKFLOW_STATE_BATCH_BYTES) throw new TypeError('Workflow state validation batch exceeds its byte limit');
      await validateWorkflowStates(saved, reports);
    }
    return body;
  }, body => validate(body, request.resultMaxBytes));
}
