import { AsyncLocalStorage } from 'node:async_hooks';
import { randomUUID } from 'node:crypto';
import { operationAPIBase, operationResponse, operationJSON, type Operation, type OperationReport, type OperationArtifactReport, type OperationArtifact } from './customer-operations.js';
import { prepareOperationArtifact, type OperationArtifactInput, type PreparedOperationArtifact } from './operation-artifact.js';
import { OperationControlScope, validateOperationControl, type OperationHandlerScope } from './operation-control.js';
import { operationUploader, type OperationDirectUploadInput } from './operation-upload.js';
import type { OperationArtifactUploadRequest } from './generated/models/OperationArtifactUploadRequest.js';
import type { OperationExecutionControlResponse } from './generated/models/OperationExecutionControlResponse.js';

export type { OperationArtifactInput, OperationArtifactUpload, PreparedOperationArtifact } from './operation-artifact.js';
export { OperationStoppedError, type OperationStopCode, type OperationHandlerScope } from './operation-control.js';
export type { OperationExecutionControlResponse } from './generated/models/OperationExecutionControlResponse.js';

export type { OperationDirectUploadInput } from './operation-upload.js';

export interface OperationExecutionContext { id: string; attempt: number; capability: string; invocationID: string }
export interface GregaleOperationsOptions { apiURL: string; identityEndpoint?: string; fetch?: typeof globalThis.fetch; timeoutMs?: number }
export type OperationRequestHeaders = HeadersInit | Record<string, string | string[] | undefined>;

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
    this.identity.searchParams.set('audience', 'gregale:operations'); this.fetchImpl = options.fetch ?? globalThis.fetch;
    this.timeout = options.timeoutMs ?? 5000;
    if (!Number.isFinite(this.timeout) || this.timeout <= 0 || this.timeout > 10000) throw new Error('Invalid operation report timeout');
  }
  context(): Readonly<Omit<OperationExecutionContext, 'capability'>> | undefined {
    const context = this.request.getStore();
    return context ? { id: context.id, attempt: context.attempt, invocationID: context.invocationID } : undefined;
  }
  runRequest<T>(headers: OperationRequestHeaders, handler: () => T | Promise<T>): T | Promise<T> {
    const normalized = headers instanceof Headers || Array.isArray(headers) ? new Headers(headers) : new Headers(Object.entries(headers).filter((entry): entry is [string, string | string[]] => entry[1] !== undefined).map(([key, value]) => [key, Array.isArray(value) ? value.join(', ') : value]));
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
      } catch (error) { this.finished.add(context); throw error; }
    });
  }
  async progress(report: Omit<OperationReport, 'report_id'> & { report_id?: string }): Promise<Operation> {
    return this.report('progress', { ...report, report_id: report.report_id ?? randomUUID() });
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
    return this.report('artifacts', { ...artifact, report_id: artifact.report_id ?? randomUUID() });
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
        guard, checkpoint: async () => { guard(); if (control) await control.checkpoint(); },
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
  private async report(path: 'progress' | 'artifacts', body: OperationReport | OperationArtifactReport): Promise<Operation> {
    const execution = this.requireExecution();
    const control = this.controls.get(execution);
    control?.throwIfStopped();
    const operation = await this.runtimeRequest(path, body, control?.controller.signal) as Operation;
    control?.throwIfStopped();
    return operation;
  }
  private requireExecution(expected?: OperationExecutionContext): OperationExecutionContext {
    const execution = this.request.getStore();
    if (!execution || (expected && expected !== execution) || this.finished.has(execution)) throw new Error('Reporting or control requires an operation execution in its original request');
    return execution;
  }
  private async runtimeRequest(path: 'progress' | 'artifacts' | 'control' | 'artifact-upload-receipts' | 'artifact-uploads', body?: OperationReport | OperationArtifactReport | OperationArtifactUploadRequest, callerSignal?: AbortSignal, bytes?: Buffer): Promise<unknown> {
    const execution = this.requireExecution();
    const timeout = AbortSignal.timeout(this.timeout);
    const signal = callerSignal ? AbortSignal.any([callerSignal, timeout]) : timeout;
    // Fetch fresh metadata for each report, including after park/restore.
    const identity = await this.fetchImpl(this.identity, { signal, cache: 'no-store', redirect: 'error' });
    if (!identity.ok) throw new Error('Operation workload identity unavailable');
    const token = await operationJSON(identity) as { access_token?: string };
    if (typeof token.access_token !== 'string' || !token.access_token || token.access_token.length > 8192) throw new Error('Invalid operation workload identity');
    signal.throwIfAborted();
    this.requireExecution(execution);
    const url = new URL(`/v1/runtime/operations/${execution.id}/${path}`, this.api);
    if (bytes && body) {
      const declaration = body as OperationArtifactUploadRequest;
      url.search = new URLSearchParams({report_id: declaration.report_id, name: declaration.name, size_bytes: String(declaration.size_bytes), sha256: declaration.sha256}).toString();
    }
    const response = await this.fetchImpl(url, { method: path === 'control' ? 'GET' : 'POST', signal, cache: 'no-store', redirect: 'error', credentials: 'omit',
      headers: { Authorization: `Bearer ${token.access_token}`, ...(body ? { 'Content-Type': bytes ? 'application/octet-stream' : 'application/json' } : {}), ...operationExecutionHeaders(execution) },
      ...(body ? { body: bytes ?? JSON.stringify(body) } : {}) });
    const result = await operationResponse<unknown>(response);
    signal.throwIfAborted();
    this.requireExecution(execution);
    return result;
  }
}

function operationExecutionContext(headers: Headers, id: string): OperationExecutionContext {
  const invocationID = headers.get('X-Faas-Invocation-Id') ?? '';
  const attemptRaw = headers.get('X-Gregale-Operation-Attempt') ?? '';
  const capability = headers.get('X-Gregale-Operation-Capability') ?? '';
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
  const positive = (value: string): boolean => /^[1-9][0-9]*$/.test(value) && Number.isSafeInteger(Number(value));
  const invalid = (): never => { throw new Error('Invalid operation execution context'); };
  if (!uuid.test(id) || !positive(attemptRaw) || !/^[0-9a-f]{64}$/.test(capability)) return invalid();
  headers.forEach((_value, name) => {
    if (name.startsWith('x-gregale-operation-') && !['x-gregale-operation-attempt', 'x-gregale-operation-capability'].includes(name)) invalid();
  });
  if (!uuid.test(invocationID)) return invalid();
  return { id, attempt: Number(attemptRaw), capability, invocationID };
}

function operationExecutionHeaders(execution: OperationExecutionContext): Record<string, string> {
  return { 'X-Gregale-Operation-Attempt': String(execution.attempt), 'X-Gregale-Operation-Capability': execution.capability, 'X-Faas-Invocation-Id': execution.invocationID };
}

export { GregaleWorkflowOperations, type WorkflowOperationContext, type WorkflowArtifactReceipt } from './workflow-operations-runtime.js';
export type { OperationWorkflowControlResponse } from './generated/models/OperationWorkflowControlResponse.js';
