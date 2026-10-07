// ADR-644/647: app-owned workflow state snapshots with transactional continuity.
import { randomUUID } from 'node:crypto';
import { OPERATION_MILESTONE_BATCH_BYTES, OPERATION_WORKFLOW_STATE_REPORTS } from './operation-contract.js';
import type { CustomerOperationTransactionRequest } from './customer-operation-transactions.js';
import { customerOperationRequestDigest } from './customer-operation-transactions.js';
import type { OperationPool, OperationTransaction } from './operation-receipt.js';

export interface OperationWorkflowStateReport {
  id: string;
  workflow: string;
  instance_id: string;
  from_state?: string;
  state: string;
  revision: number;
  occurred_at: string;
}

export interface OperationWorkflowStateReceipt extends Omit<OperationWorkflowStateReport, 'occurred_at'> {
  operation_id: string;
}

const WORKFLOW = /^[a-z][a-z0-9-]{0,62}$/;
const STATE = /^[a-z][a-z0-9-]{0,63}$/;

export function workflowStateTransaction(
  request: CustomerOperationTransactionRequest,
  reports: OperationWorkflowStateReport[],
  isOpen: () => boolean,
): {
  workflowState(workflow: string, instanceID: string, state: string): void;
  workflowTransition(workflow: string, instanceID: string, fromState: string, toState: string): void;
} {
  const queue = (workflow: string, instanceID: string, state: string, fromState?: string) => {
    if (!isOpen()) throw new TypeError('Workflow state must be reported inside the transaction callback');
    if (!request.milestonesSupported) throw new TypeError('Customer Operation workflow states are not negotiated');
    if (typeof workflow !== 'string' || !WORKFLOW.test(workflow) || typeof state !== 'string' || !STATE.test(state)
        || fromState !== undefined && (typeof fromState !== 'string' || !STATE.test(fromState))
        || typeof instanceID !== 'string' || !instanceID || Buffer.byteLength(instanceID) > 256
        || /[\x00-\x1f\x7f]/.test(instanceID) || /[\uD800-\uDFFF]/u.test(instanceID)) {
      throw new TypeError('Workflow state requires valid workflow, instance ID, and state names');
    }
    if (reports.length >= OPERATION_WORKFLOW_STATE_REPORTS) throw new TypeError('Too many workflow state updates');
    const report: OperationWorkflowStateReport = {
      id: randomUUID(), workflow, instance_id: instanceID, state, revision: 0, occurred_at: new Date().toISOString(),
    };
    if (fromState !== undefined) report.from_state = fromState;
    if (Buffer.byteLength(JSON.stringify({workflow_states: [...reports, report]})) > OPERATION_MILESTONE_BATCH_BYTES) throw new TypeError('Workflow state batch exceeds its byte limit');
    reports.push(report);
  };
  return {
    workflowState(workflow, instanceID, state) { queue(workflow, instanceID, state); },
    workflowTransition(workflow, instanceID, fromState, toState) { queue(workflow, instanceID, toState, fromState); },
  };
}

export async function saveCustomerWorkflowStates(
  tx: OperationTransaction,
  request: CustomerOperationTransactionRequest,
  reports: OperationWorkflowStateReport[],
): Promise<OperationWorkflowStateReport[]> {
  const saved: OperationWorkflowStateReport[] = [];
  for (const report of reports) {
    const counter = (await tx.query(
      `INSERT INTO public.gregale_customer_operation_workflow_state_counters(platform_tenant_id,workflow,instance_id,revision)
       VALUES ($1::uuid,$2,$3,1)
       ON CONFLICT (platform_tenant_id,workflow,instance_id) DO UPDATE
        SET revision=public.gregale_customer_operation_workflow_state_counters.revision+1
        WHERE public.gregale_customer_operation_workflow_state_counters.revision<9007199254740991
       RETURNING revision`, [request.platformTenantId, report.workflow, report.instance_id],
    )).rows[0];
    const revision = Number(counter?.revision);
    if (!Number.isSafeInteger(revision) || revision < 1) throw new TypeError('Workflow state revision is unavailable');
    const value = {...report, revision};
    // The counter upsert above serializes writers for this run until commit.
    // Its head survives receipt cleanup, so the check does not depend on old
    // outbox rows remaining in the application database.
    const head = (await tx.query(
      `SELECT last_state FROM public.gregale_customer_operation_workflow_state_counters
       WHERE platform_tenant_id=$1::uuid AND workflow=$2 AND instance_id=$3 FOR UPDATE`,
      [request.platformTenantId, value.workflow, value.instance_id],
    )).rows[0];
    if (head === undefined) throw new TypeError('Workflow state counter is unavailable');
    const priorState = head.last_state;
    if (priorState !== null && priorState !== undefined && (typeof priorState !== 'string' || !STATE.test(priorState))) {
      throw new TypeError('Saved workflow state is invalid');
    }
    if (value.from_state !== undefined && typeof priorState === 'string' && priorState !== value.from_state) {
      throw new TypeError('Workflow transition source does not match the latest app-reported state');
    }
    await tx.query(
      `INSERT INTO public.gregale_customer_operation_workflow_states(operation_id,id,platform_tenant_id,workflow,instance_id,from_state,state,revision,occurred_at)
       VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9::timestamptz)`,
      [request.operationId, value.id, request.platformTenantId, value.workflow, value.instance_id, value.from_state ?? '', value.state, value.revision, value.occurred_at],
    );
    const updatedHead = (await tx.query(
      `UPDATE public.gregale_customer_operation_workflow_state_counters SET last_state=$4
       WHERE platform_tenant_id=$1::uuid AND workflow=$2 AND instance_id=$3 AND revision=$5
       RETURNING revision`,
      [request.platformTenantId, value.workflow, value.instance_id, value.state, value.revision],
    )).rows[0];
    if (Number(updatedHead?.revision) !== value.revision) throw new TypeError('Workflow state counter changed unexpectedly');
    saved.push(value);
  }
  return saved;
}

export class OperationWorkflowStatePublicationError extends Error {
  readonly code = 'operation_workflow_state_publication_incomplete';
  readonly committed = true;
  constructor(cause: unknown) {
    super('Business transaction committed; workflow state publication incomplete. Retry the same operation.', {cause});
    this.name = 'OperationWorkflowStatePublicationError';
  }
}

export async function publishCustomerWorkflowStates(
  pool: OperationPool,
  request: CustomerOperationTransactionRequest,
  publish: (report: OperationWorkflowStateReport) => Promise<OperationWorkflowStateReceipt>,
): Promise<void> {
  if (!request.milestonesSupported) return;
  let connection;
  try {
    connection = await pool.connect();
    const digest = Buffer.from(customerOperationRequestDigest(request));
    const values = [request.operationId, request.accountId, request.appId, request.platformTenantId, digest];
    const rows = (await connection.query(
      `SELECT s.id::text,s.workflow,s.instance_id,s.from_state,s.state,s.revision,s.occurred_at FROM public.gregale_customer_operation_workflow_states s
       JOIN public.gregale_customer_operation_inbox r ON r.operation_id=s.operation_id
       WHERE r.operation_id=$1::uuid AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid
        AND r.request_digest=$5 AND s.acknowledged_at IS NULL ORDER BY s.revision,s.id LIMIT $6`, [...values, OPERATION_WORKFLOW_STATE_REPORTS + 1],
    )).rows;
    if (rows.length > OPERATION_WORKFLOW_STATE_REPORTS) throw new TypeError('Saved workflow state count exceeds its bound');
    for (const row of rows) {
      const revision = Number(row.revision);
      if (typeof row.id !== 'string' || typeof row.workflow !== 'string' || typeof row.instance_id !== 'string'
          || typeof row.state !== 'string' || typeof row.from_state !== 'string' || !Number.isSafeInteger(revision) || revision < 1) throw new TypeError('Invalid saved workflow state');
      const date = row.occurred_at instanceof Date ? row.occurred_at : new Date(String(row.occurred_at));
      if (!Number.isFinite(date.valueOf())) throw new TypeError('Invalid saved workflow state timestamp');
      const report: OperationWorkflowStateReport = {id: row.id, workflow: row.workflow, instance_id: row.instance_id, state: row.state, revision, occurred_at: date.toISOString()};
      if (row.from_state) report.from_state = row.from_state;
      const receipt = await publish(report);
      if (receipt?.id !== report.id || receipt.operation_id !== request.operationId || receipt.revision !== revision
          || receipt.workflow !== report.workflow || receipt.instance_id !== report.instance_id || receipt.from_state !== report.from_state || receipt.state !== report.state) {
        throw new TypeError('Workflow state publication identity was not confirmed');
      }
      await connection.query(
        `UPDATE public.gregale_customer_operation_workflow_states s SET acknowledged_at=clock_timestamp()
         FROM public.gregale_customer_operation_inbox r WHERE s.operation_id=r.operation_id AND r.operation_id=$1::uuid
          AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid AND r.request_digest=$5 AND s.id=$6::uuid`, [...values, row.id],
      );
    }
  } catch (error) { throw new OperationWorkflowStatePublicationError(error); }
  finally { connection?.release(); }
}
