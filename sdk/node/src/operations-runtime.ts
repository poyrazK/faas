import { AsyncLocalStorage } from 'node:async_hooks';
import { randomUUID } from 'node:crypto';
import { operationAPIBase, operationResponse, operationJSON, type Operation, type OperationReport, type OperationArtifactReport } from './customer-operations.js';
import { operationHeaders, operationExecutionContext, type OperationExecutionContext, type OperationRequestHeaders } from './operation-execution-context.js';
import { customerOperationRequestFromHeaders, withCustomerOperationTransaction, type CustomerOperationHTTPRequest } from './customer-operation-transactions.js';
import { publishCustomerMilestones, type CustomerOperationTransaction } from './customer-operation-milestones.js';
import { publishCustomerWorkflowStates, type OperationWorkflowStateReport } from './customer-operation-workflow-states.js';
import type { OperationMilestone, OperationMilestoneReport } from './customer-operations.js';
import type { OperationPool, OperationTransactionResult } from './operation-receipt.js';
export type { OperationExecutionContext, OperationRequestHeaders } from './operation-execution-context.js';

export interface GregaleOperationsOptions { apiURL: string; identityEndpoint?: string; fetch?: typeof globalThis.fetch; timeoutMs?: number }

/** Ordinary HTTP handler integration. Gregale reserves these headers;
 * use runRequest only on requests delivered by Gregale's guest listener. */
export class GregaleOperations {
  private readonly request = new AsyncLocalStorage<OperationExecutionContext | undefined>();
  private readonly api: URL;
  private readonly identity: URL;
  private readonly fetchImpl: typeof globalThis.fetch;
  private readonly timeout: number;
  constructor(options: GregaleOperationsOptions) {
    this.api = operationAPIBase(options.apiURL);
    this.identity = new URL(options.identityEndpoint ?? process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT ?? '');
    if (this.identity.username || this.identity.password || this.identity.hash || this.identity.protocol !== 'http:' || !['127.0.0.1', '[::1]', 'localhost'].includes(this.identity.hostname)) throw new Error('Workload identity requires loopback HTTP');
    this.identity.searchParams.set('audience', 'gregale:operations'); this.fetchImpl = options.fetch ?? globalThis.fetch;
    this.timeout = options.timeoutMs ?? 5000;
    if (!Number.isFinite(this.timeout) || this.timeout <= 0 || this.timeout > 10000) throw new Error('Invalid operation report timeout');
  }
  context(): Readonly<Omit<OperationExecutionContext, 'capability'>> | undefined {
    const context = this.request.getStore();
    return context ? { id: context.id, attempt: context.attempt, invocationID: context.invocationID } : undefined;
  }
  runRequest<T>(headers: OperationRequestHeaders, handler: () => T | Promise<T>): T | Promise<T> {
    const normalized = operationHeaders(headers);
    const id = normalized.get('X-Gregale-Customer-Operation-Id');
    if (!id) return this.request.run(undefined, handler);
    const context = operationExecutionContext(normalized, id);
    return this.request.run(context, handler);
  }
  /** Authorize business access before calling; replay skips the transaction callback. */
  async transaction(
    request: CustomerOperationHTTPRequest, pool: OperationPool,
    handler: (transaction: CustomerOperationTransaction) => Promise<unknown>,
  ): Promise<OperationTransactionResult> {
    const input = customerOperationRequestFromHeaders(request.headers, request.method, request.path, request.body);
    return this.runRequest(request.headers, async () => {
      const receipt = await withCustomerOperationTransaction(pool, input, handler, async reports => {
        const validation = await this.report<{valid: boolean}>('milestones/validate', {milestones: reports});
        if (validation?.valid !== true) throw new TypeError('Milestone validation was not confirmed');
      }, async reports => {
        const validation = await this.report<{valid: boolean}>('workflow-states/validate', {workflow_states: reports});
        if (validation?.valid !== true) throw new TypeError('Workflow state validation was not confirmed');
      });
      await publishCustomerMilestones(pool, input, report => this.milestone(report));
      await publishCustomerWorkflowStates(pool, input, report => this.workflowState(report));
      return receipt;
    });
  }
  async progress(report: Omit<OperationReport, 'report_id'> & { report_id?: string }): Promise<Operation> {
    return this.report('progress', { ...report, report_id: report.report_id ?? randomUUID() });
  }
  async artifact(artifact: Omit<OperationArtifactReport, 'report_id'> & { report_id?: string }): Promise<Operation> {
    return this.report('artifacts', { ...artifact, report_id: artifact.report_id ?? randomUUID() });
  }
  /** Only publish facts already committed by your application. Use transaction for durable replay. */
  async milestone(report: OperationMilestoneReport): Promise<OperationMilestone> {
    return this.report<OperationMilestone>('milestones', report);
  }
  /** Publish an app transaction's committed workflow state snapshot. */
  async workflowState(report: OperationWorkflowStateReport): Promise<import('./customer-operation-workflow-states.js').OperationWorkflowStateReceipt> {
    return this.report('workflow-states', report);
  }
  private async report<T = Operation>(path: 'progress' | 'artifacts' | 'milestones' | 'milestones/validate' | 'workflow-states' | 'workflow-states/validate', body: unknown): Promise<T> {
    const execution = this.request.getStore();
    if (!execution) throw new Error('Progress or artifact reporting requires an operation execution');
    const signal = AbortSignal.timeout(this.timeout);
    // Fetch fresh metadata for each report, including after park/restore.
    const identity = await this.fetchImpl(this.identity, { signal, cache: 'no-store', redirect: 'error' });
    if (!identity.ok) throw new Error('Operation workload identity unavailable');
    const token = await operationJSON(identity) as { access_token?: string };
    if (typeof token.access_token !== 'string' || !token.access_token || token.access_token.length > 8192) throw new Error('Invalid operation workload identity');
    const response = await this.fetchImpl(new URL(`/v1/runtime/operations/${execution.id}/${path}`, this.api), { method: 'POST', signal, cache: 'no-store', redirect: 'error',
      headers: { Authorization: `Bearer ${token.access_token}`, 'Content-Type': 'application/json', ...operationExecutionHeaders(execution) }, body: JSON.stringify(body) });
    return operationResponse<T>(response);
  }
}

function operationExecutionHeaders(execution: OperationExecutionContext): Record<string, string> {
  return { 'X-Gregale-Operation-Attempt': String(execution.attempt), 'X-Gregale-Operation-Capability': execution.capability, 'X-Faas-Invocation-Id': execution.invocationID };
}
