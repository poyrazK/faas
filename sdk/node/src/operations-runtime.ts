import { AsyncLocalStorage } from 'node:async_hooks';
import { randomUUID } from 'node:crypto';
import { operationAPIBase, operationResponse, operationJSON, type Operation, type OperationReport, type OperationArtifactReport, type OperationArtifact, type OperationMilestone, type OperationMilestoneReport } from './customer-operations.js';
import { prepareOperationArtifact, type OperationArtifactInput, type PreparedOperationArtifact } from './operation-artifact.js';
import { OperationControlScope, validateOperationControl, type OperationHandlerScope } from './operation-control.js';
import { operationUploader, type OperationDirectUploadInput } from './operation-upload.js';
import type { OperationArtifactUploadRequest } from './generated/models/OperationArtifactUploadRequest.js';
import type { OperationExecutionControlResponse } from './generated/models/OperationExecutionControlResponse.js';
import { operationHeaders, operationExecutionContext, type OperationExecutionContext, type OperationRequestHeaders } from './operation-execution-context.js';
import { customerOperationRequestFromHeaders, withCustomerOperationTransaction, type CustomerOperationHTTPRequest } from './customer-operation-transactions.js';
import { publishCustomerMilestones, type CustomerOperationTransaction } from './customer-operation-milestones.js';
import { publishCustomerWorkflowStates, type OperationWorkflowStateReceipt, type OperationWorkflowStateReport } from './customer-operation-workflow-states.js';
import type { OperationPool, OperationTransactionResult } from './operation-receipt.js';

export type { OperationExecutionContext, OperationRequestHeaders } from './operation-execution-context.js';
export type { OperationArtifactInput, OperationArtifactUpload, PreparedOperationArtifact } from './operation-artifact.js';
export { OperationStoppedError, type OperationStopCode, type OperationHandlerScope } from './operation-control.js';
export type { OperationExecutionControlResponse } from './generated/models/OperationExecutionControlResponse.js';
export type { OperationDirectUploadInput } from './operation-upload.js';

export interface GregaleOperationsOptions { apiURL: string; identityEndpoint?: string; fetch?: typeof globalThis.fetch; timeoutMs?: number }

/** Ordinary HTTP handler integration. Gregale reserves these headers;
 * use runRequest only on requests delivered by Gregale's guest listener. */
export class GregaleOperations {
  private readonly request = new AsyncLocalStorage<OperationExecutionContext | undefined>();
  private readonly finished = new WeakSet<OperationExecutionContext>();
  private readonly controls = new WeakMap<OperationExecutionContext, OperationControlScope>();
  private readonly uploads = new WeakMap<OperationExecutionContext, ReturnType<typeof operationUploader>>();
  private readonly api: URL;
  private readonly identity: URL;
  private readonly fetchImpl: typeof globalThis.fetch;
  private readonly timeout: number;

  constructor(options: GregaleOperationsOptions) {
    this.api = operationAPIBase(options.apiURL);
    this.identity = new URL(options.identityEndpoint ?? process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT ?? '');
    if (this.identity.username || this.identity.password || this.identity.hash || this.identity.protocol !== 'http:' || !['127.0.0.1', '[::1]', 'localhost'].includes(this.identity.hostname)) throw new Error('Workload identity requires loopback HTTP');
    this.identity.searchParams.set('audience', 'gregale:operations');
    this.fetchImpl = options.fetch ?? globalThis.fetch;
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
    return this.request.run(context, () => {
      try {
        const result = handler();
        if (result !== null && (typeof result === 'object' || typeof result === 'function') && typeof (result as Promise<T>).then === 'function') {
          return Promise.resolve(result).finally(() => { this.finished.add(context); });
        }
        this.finished.add(context);
        return result;
      } catch (error) {
        this.finished.add(context);
        throw error;
      }
    });
  }

  /** Authorize business access before calling; replay skips the transaction callback. */
  async transaction(
    request: CustomerOperationHTTPRequest,
    pool: OperationPool,
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
    return this.report('progress', {...report, report_id: report.report_id ?? randomUUID()});
  }

  /** Only publish facts already committed by your application. Use transaction for durable replay. */
  async milestone(report: OperationMilestoneReport): Promise<OperationMilestone> {
    return this.report('milestones', report);
  }

  /** Publish an app transaction's committed workflow state snapshot. */
  async workflowState(report: OperationWorkflowStateReport): Promise<OperationWorkflowStateReceipt> {
    return this.report('workflow-states', report);
  }

  /** Opt in to cooperative cancellation for a trusted ordinary HTTP operation.
   * Pass scope.signal to I/O and call checkpoints between units of work. */
  async runCancellableRequest<T>(headers: OperationRequestHeaders, handler: (scope: OperationHandlerScope) => T | Promise<T>): Promise<T> {
    return this.runRequest(headers, async () => {
      const execution = this.request.getStore();
      if (!execution) throw new Error('Trusted operation execution required');
      const control = new OperationControlScope(signal => this.control(signal), () => this.requireExecution(execution));
      this.controls.set(execution, control);
      // Keep the stopped scope in this weak map through runRequest's finalizer;
      // detached microtasks must not regain report/write authority during cleanup.
      return control.run(handler);
    });
  }

  /** Read cancellation and the current lease without renewing or settling work. */
  async control(signal?: AbortSignal): Promise<OperationExecutionControlResponse> {
    const execution = this.requireExecution();
    return validateOperationControl(await this.runtimeRequest('control', undefined, signal) as OperationExecutionControlResponse, execution);
  }

  async artifact(artifact: Omit<OperationArtifactReport, 'report_id'> & { report_id?: string }): Promise<Operation> {
    return this.report('artifacts', {...artifact, report_id: artifact.report_id ?? randomUUID()});
  }

  /** Freeze bounded file bytes and one report identity inside this HTTP request.
   * Upload credentials stay with the application's existing bucket writer. */
  prepareArtifact(input: OperationArtifactInput): PreparedOperationArtifact {
    const execution = this.request.getStore();
    const guard = (): void => {
      if (!execution || this.request.getStore() !== execution || this.finished.has(execution)) throw new Error('Artifact preparation and attachment require the original operation request');
      this.controls.get(execution)?.throwIfStopped();
    };
    return prepareOperationArtifact(input, guard, async report => {
      const operation = await this.artifact(report);
      if (operation.id !== execution?.id) throw new Error('Artifact response identity changed');
      return operation;
    }, undefined, () => execution ? this.controls.get(execution)?.controller.signal : undefined);
  }

  /** Upload bounded private bytes with this invocation's workload identity.
   * A stable report ID recovers a lost acknowledgement without rerunning work. */
  async uploadArtifact(input: OperationDirectUploadInput): Promise<OperationArtifact> {
    const execution = this.requireExecution();
    const control = this.controls.get(execution);
    const guard = (): void => { this.requireExecution(execution); control?.throwIfStopped(); };
    guard();
    let upload = this.uploads.get(execution);
    if (!upload) {
      upload = operationUploader(execution.id, {
        guard,
        checkpoint: async () => { guard(); if (control) await control.checkpoint(); },
        aborted: () => control?.controller.signal.aborted ?? false,
        lookup: async declaration => this.runtimeRequest('artifact-upload-receipts', declaration, control?.controller.signal) as Promise<{available: boolean; artifact?: OperationArtifact}>,
        upload: async (declaration, bytes) => this.runtimeRequest('artifact-uploads', declaration, control?.controller.signal, bytes) as Promise<{available: boolean; artifact?: OperationArtifact}>,
      });
      this.uploads.set(execution, upload);
    }
    const artifact = await upload(input);
    guard();
    return artifact;
  }

  private async report<T = Operation>(
    path: 'progress' | 'artifacts' | 'milestones' | 'milestones/validate' | 'workflow-states' | 'workflow-states/validate',
    body: unknown,
  ): Promise<T> {
    const execution = this.requireExecution();
    const control = this.controls.get(execution);
    control?.throwIfStopped();
    const result = await this.runtimeRequest(path, body, control?.controller.signal) as T;
    control?.throwIfStopped();
    return result;
  }

  private requireExecution(expected?: OperationExecutionContext): OperationExecutionContext {
    const execution = this.request.getStore();
    if (!execution || (expected && expected !== execution) || this.finished.has(execution)) throw new Error('Reporting or control requires an operation execution in its original request');
    return execution;
  }

  private async runtimeRequest(
    path: 'progress' | 'artifacts' | 'control' | 'milestones' | 'milestones/validate' | 'workflow-states' | 'workflow-states/validate' | 'artifact-upload-receipts' | 'artifact-uploads',
    body?: unknown,
    callerSignal?: AbortSignal,
    bytes?: Buffer,
  ): Promise<unknown> {
    const execution = this.requireExecution();
    const timeout = AbortSignal.timeout(this.timeout);
    const signal = callerSignal ? AbortSignal.any([callerSignal, timeout]) : timeout;
    // Fetch fresh metadata for each report, including after park/restore.
    const identity = await this.fetchImpl(this.identity, {signal, cache: 'no-store', redirect: 'error'});
    if (!identity.ok) throw new Error('Operation workload identity unavailable');
    const token = await operationJSON(identity) as {access_token?: string};
    if (typeof token.access_token !== 'string' || !token.access_token || token.access_token.length > 8192) throw new Error('Invalid operation workload identity');
    signal.throwIfAborted();
    this.requireExecution(execution);
    const url = new URL(`/v1/runtime/operations/${execution.id}/${path}`, this.api);
    if (bytes && body) {
      const declaration = body as OperationArtifactUploadRequest;
      url.search = new URLSearchParams({report_id: declaration.report_id, name: declaration.name, size_bytes: String(declaration.size_bytes), sha256: declaration.sha256}).toString();
    }
    const response = await this.fetchImpl(url, {
      method: path === 'control' ? 'GET' : 'POST', signal, cache: 'no-store', redirect: 'error', credentials: 'omit',
      headers: {
        Authorization: `Bearer ${token.access_token}`,
        ...(body ? {'Content-Type': bytes ? 'application/octet-stream' : 'application/json'} : {}),
        ...operationExecutionHeaders(execution),
      },
      ...(body ? {body: bytes ?? JSON.stringify(body)} : {}),
    });
    const result = await operationResponse<unknown>(response);
    signal.throwIfAborted();
    this.requireExecution(execution);
    return result;
  }
}

function operationExecutionHeaders(execution: OperationExecutionContext): Record<string, string> {
  return {'X-Gregale-Operation-Attempt': String(execution.attempt), 'X-Gregale-Operation-Capability': execution.capability, 'X-Faas-Invocation-Id': execution.invocationID};
}

export { GregaleWorkflowOperations, type WorkflowOperationContext, type WorkflowArtifactReceipt } from './workflow-operations-runtime.js';
export type { OperationWorkflowControlResponse } from './generated/models/OperationWorkflowControlResponse.js';
