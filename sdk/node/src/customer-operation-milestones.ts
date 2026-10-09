import { businessCompensationPayload, type OperationBusinessCompensation } from './customer-operation-compensation.js';
import { businessEffectPayload, type OperationBusinessEffect } from './customer-operation-effects.js';
import { applyBusinessInvariant, businessInvariantPayload, type OperationBusinessInvariant } from './customer-operation-invariants.js';
import { reconcileWorkflowState, type CustomerOperationReconciliationHelper } from './customer-operation-reconciliation.js';
import { businessDecisionPayload, type OperationBusinessDecision } from './customer-operation-decisions.js';
// ADR-715: commit facts with business writes, then publish under a fresh execution fence.
import { guardedWorkflowTransition, type CustomerOperationReadinessGuard } from './customer-operation-readiness.js';
import { randomUUID } from 'node:crypto';
import { OPERATION_MILESTONE_PAYLOAD_BYTES, OPERATION_MILESTONE_BATCH_BYTES, OPERATION_MILESTONES } from './operation-contract.js';
import { customerOperationRequestDigest, type CustomerOperationTransactionRequest } from './customer-operation-transactions.js';
import type { OperationConnection, OperationPool, OperationTransaction } from './operation-receipt.js';
import type { OperationMilestone, OperationMilestoneReport } from './customer-operations.js';
import { workflowStateTransaction, type OperationWorkflowStateReport, type OperationWorkflowDependency, type OperationWorkflowBlocker, type OperationWorkflowBlockerResolution } from './customer-operation-workflow-states.js';

export interface CustomerOperationTransaction extends OperationTransaction {
  businessCompensation(milestone: string, compensation: OperationBusinessCompensation): void;
  businessEffect(milestone: string, effect: OperationBusinessEffect): void;
  reportBusinessInvariant(milestone: string, input: OperationBusinessInvariant, current: OperationWorkflowBlocker[]): OperationWorkflowBlocker[];
  reconcileWorkflowState: CustomerOperationReconciliationHelper;
  businessDecision(milestone: string, decision: OperationBusinessDecision): void;
  guardedWorkflowTransition: CustomerOperationReadinessGuard;
  /** Declared public fact. Queued synchronously and saved before the transaction commits. */
  milestone(name: string, payload: unknown): void;
  /** Explicit business state. Revisions are assigned before commit and published after commit. */
  workflowDependencies(workflow: string, instanceID: string, state: string, dependencies: OperationWorkflowDependency[]): void;
  workflowOutcome(workflow: string, instanceID: string, state: string, code: string, description: string): void;
  workflowDeadline(workflow: string, instanceID: string, state: string, dueAt: string): void;
  workflowBlockers(workflow: string, instanceID: string, state: string, blockers: OperationWorkflowBlocker[], resolutions?: OperationWorkflowBlockerResolution[]): void;
  workflowState(workflow: string, instanceID: string, state: string): void;
  /** Declared state transition. Check that fromState matches the locked business row. */
  workflowTransition(workflow: string, instanceID: string, fromState: string, toState: string): void;
}
export class OperationMilestonePublicationError extends Error {
  readonly code = 'operation_milestone_publication_incomplete';
  readonly committed = true;
  constructor(cause: unknown) {
    super('Business transaction committed; milestone publication incomplete. Retry the same operation.', {cause});
    this.name = 'OperationMilestonePublicationError';
  }
}

export function milestoneJSON(value: unknown): string {
  const encoded = JSON.stringify(value, (_key, item: unknown) => {
    if (item === undefined || typeof item === 'function' || typeof item === 'symbol' || typeof item === 'bigint' || (typeof item === 'number' && !Number.isFinite(item))) throw new TypeError('Milestone payload must contain JSON values');
    return item;
  });
  if (typeof encoded !== 'string' || Buffer.byteLength(encoded) > OPERATION_MILESTONE_PAYLOAD_BYTES) throw new TypeError('Milestone payload exceeds its byte limit');
  return encoded;
}

export function milestoneTransaction(tx: OperationTransaction, request: CustomerOperationTransactionRequest, reports: OperationMilestoneReport[], workflowStates: OperationWorkflowStateReport[], isOpen: () => boolean, failGuard: (error: unknown) => void = () => {}): CustomerOperationTransaction {
  const stateTransaction = workflowStateTransaction(request, workflowStates, isOpen);
  const transaction: CustomerOperationTransaction = {businessCompensation(name,compensation) {transaction.milestone(name,businessCompensationPayload(compensation));},businessEffect(name,effect) {transaction.milestone(name,businessEffectPayload(effect));},reportBusinessInvariant(name,input,current) {
    try {
      const payload=businessInvariantPayload(input), blockers=applyBusinessInvariant(payload.invariant,current);
      transaction.milestone(name,payload);
      transaction.workflowBlockers(input.workflow,input.instance_id,input.state,blockers);
      return blockers;
    } catch(error){failGuard(error);throw error;}
  },reconcileWorkflowState: async (...args) => reconcileWorkflowState(transaction, request.appId, reports, workflowStates, isOpen, failGuard, ...args),businessDecision(name, decision) { transaction.milestone(name, businessDecisionPayload(decision)); },guardedWorkflowTransition: async (proposal, facts, check) => guardedWorkflowTransition(transaction, request, reports, workflowStates, isOpen, failGuard, proposal, facts, check), query: tx.query.bind(tx), workflowDependencies: stateTransaction.workflowDependencies, workflowOutcome: stateTransaction.workflowOutcome, workflowDeadline: stateTransaction.workflowDeadline, workflowBlockers: stateTransaction.workflowBlockers, workflowState: stateTransaction.workflowState, workflowTransition: stateTransaction.workflowTransition, milestone(name, payload) {
    if (!isOpen()) throw new TypeError('Milestones must be recorded inside the transaction callback');
    if (!request.milestonesSupported) throw new TypeError('Customer Operation milestones are not negotiated');
    if (typeof name !== 'string' || !/^[a-z][a-z0-9-]{0,63}$/.test(name)) throw new TypeError('Milestone name must be a bounded lowercase slug');
    if (reports.length >= OPERATION_MILESTONES) throw new TypeError('Too many Operation milestones');
    const snapshot = JSON.parse(milestoneJSON(payload)) as unknown;
    const report = {id: randomUUID(), name, payload: snapshot, occurred_at: new Date().toISOString()};
    if (Buffer.byteLength(JSON.stringify({milestones: [...reports, report]})) > OPERATION_MILESTONE_BATCH_BYTES) throw new TypeError('Milestone batch exceeds its byte limit');
    reports.push(report);
  }};
  return transaction;
}

export async function saveCustomerMilestones(tx: OperationTransaction, operationID: string, reports: OperationMilestoneReport[]): Promise<void> {
  for (const report of reports) await tx.query(
    'INSERT INTO public.gregale_customer_operation_milestones(operation_id,id,name,payload,occurred_at) VALUES ($1::uuid,$2::uuid,$3,$4,$5::timestamptz)',
    [operationID, report.id, report.name, JSON.stringify(report.payload), report.occurred_at],
  );
}

/** Receipt scope/digest is checked again before reading or acknowledging public facts. */
export async function publishCustomerMilestones(pool: OperationPool, request: CustomerOperationTransactionRequest, publish: (report: OperationMilestoneReport) => Promise<OperationMilestone>): Promise<void> {
  if (!request.milestonesSupported) return;
  let connection: OperationConnection | undefined;
  try {
    connection = await pool.connect();
    const digest = Buffer.from(customerOperationRequestDigest(request));
    const values = [request.operationId, request.accountId, request.appId, request.platformTenantId, digest];
    const rows = (await connection.query(
      `SELECT m.id::text,m.name,m.payload,m.occurred_at FROM public.gregale_customer_operation_milestones m
       JOIN public.gregale_customer_operation_inbox r ON r.operation_id=m.operation_id
       WHERE r.operation_id=$1::uuid AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid
        AND r.request_digest=$5 AND m.acknowledged_at IS NULL ORDER BY m.occurred_at,m.id LIMIT $6`, [...values, OPERATION_MILESTONES + 1],
    )).rows;
    if (rows.length > OPERATION_MILESTONES) throw new TypeError('Saved milestone count exceeds its bound');
    for (const row of rows) {
      if (typeof row.id !== 'string' || typeof row.name !== 'string' || typeof row.payload !== 'string') throw new TypeError('Invalid saved milestone');
      const date = row.occurred_at instanceof Date ? row.occurred_at : new Date(String(row.occurred_at));
      const payload = JSON.parse(row.payload) as unknown;
      milestoneJSON(payload);
      const receipt = await publish({id: row.id, name: row.name, payload, occurred_at: date.toISOString()});
      if (receipt?.id !== row.id || receipt.operation_id !== request.operationId || receipt.name !== row.name) throw new TypeError('Milestone publication identity was not confirmed');
      await connection.query(
        `UPDATE public.gregale_customer_operation_milestones m SET acknowledged_at=clock_timestamp()
         FROM public.gregale_customer_operation_inbox r WHERE m.operation_id=r.operation_id AND r.operation_id=$1::uuid
          AND r.account_id=$2::uuid AND r.app_id=$3::uuid AND r.platform_tenant_id=$4::uuid AND r.request_digest=$5 AND m.id=$6::uuid`, [...values, row.id],
      );
    }
  } catch (error) { throw new OperationMilestonePublicationError(error); }
  finally { connection?.release(); }
}
