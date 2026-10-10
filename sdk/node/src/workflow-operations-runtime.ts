import { AsyncLocalStorage } from 'node:async_hooks';
import { operationAPIBase, operationJSON, operationResponse, type OperationArtifact, type OperationArtifactReport } from './customer-operations.js';
import { prepareOperationArtifact, type OperationArtifactInput, type PreparedOperationArtifact } from './operation-artifact.js';
import type { GregaleOperationsOptions, OperationRequestHeaders } from './operations-runtime.js';
import { operationHeaders } from './operation-execution-context.js';
import { OperationControlScope, validateWorkflowOperationControl, type OperationHandlerScope } from './operation-control.js';
import { operationUploader, type OperationDirectUploadInput } from './operation-upload.js';
import type { OperationArtifactUploadRequest } from './generated/models/OperationArtifactUploadRequest.js';
import type { OperationWorkflowControlResponse } from './generated/models/OperationWorkflowControlResponse.js';

export interface WorkflowOperationContext { id: string; runID: string; step: string; generation: number; attempt: number }
interface Execution extends WorkflowOperationContext { capability: string }
export interface WorkflowArtifactReceipt { available: boolean; artifact?: OperationArtifact }

/** Native workflow integration. Accept context only from Gregale's trusted guest listener. */
export class GregaleWorkflowOperations {
  private readonly request = new AsyncLocalStorage<Execution | undefined>();
  private readonly finished = new WeakSet<Execution>();
  private readonly controls = new WeakMap<Execution, OperationControlScope>();
  private readonly uploads = new WeakMap<Execution, ReturnType<typeof operationUploader>>();
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
    if (!Number.isFinite(this.timeout) || this.timeout <= 0 || this.timeout > 10000) throw new Error('Invalid workflow artifact timeout');
  }
  context(): Readonly<WorkflowOperationContext> | undefined {
    const e = this.request.getStore();
    return e ? { id: e.id, runID: e.runID, step: e.step, generation: e.generation, attempt: e.attempt } : undefined;
  }
  runRequest<T>(headers: OperationRequestHeaders, handler: () => T | Promise<T>): T | Promise<T> {
    const h = operationHeaders(headers);
    const id = h.get('X-Gregale-Customer-Operation-Id');
    if (!id) return this.request.run(undefined, handler);
    const execution = workflowExecution(h, id);
    return this.request.run(execution, () => {
      try {
        const result = handler();
        if (result !== null && (typeof result === 'object' || typeof result === 'function') && typeof (result as Promise<T>).then === 'function') return Promise.resolve(result).finally(() => { this.finished.add(execution); });
        this.finished.add(execution); return result;
      } catch (error) { this.finished.add(execution); throw error; }
    });
  }
  /** Observe scope.signal during I/O and checkpoint between units of work. */
  async runCancellableRequest<T>(headers: OperationRequestHeaders, handler: (scope: OperationHandlerScope) => T | Promise<T>): Promise<T> {
    return this.runRequest(headers, async () => {
      const execution = this.requireExecution();
      const control = new OperationControlScope(signal => this.control(signal), () => { this.requireExecution(execution); });
      this.controls.set(execution, control);
      return control.run(handler);
    });
  }
  /** Read native authority and time bounds without renewing or settling it. */
  async control(signal?: AbortSignal): Promise<OperationWorkflowControlResponse> {
    const execution = this.requireExecution();
    return validateWorkflowOperationControl(await this.runtimeRequest('control', undefined, signal) as OperationWorkflowControlResponse, execution);
  }
  /** Reuse a stable report_id and object URI across approved resumes.
   * Check a durable copy before writing; never retry an uncertain write from this object. */
  prepareArtifact(input: OperationArtifactInput & { report_id: string }): PreparedOperationArtifact<OperationArtifact> {
    const execution = this.requireExecution();
    if (!input.report_id) throw new Error('Workflow artifacts require a stable report ID');
    return prepareOperationArtifact(input, () => { this.requireExecution(execution); this.controls.get(execution)?.throwIfStopped(); },
      async report => {
        const receipt = await this.artifactRequest('artifacts', report);
        if (!receipt.available || !receipt.artifact) throw new Error('Verified workflow artifact receipt required');
        return receipt.artifact;
      }, async report => (await this.artifactRequest('artifact-receipts', report)).artifact, () => this.controls.get(execution)?.controller.signal);
  }
  /** Retain bounded private bytes for the final workflow action. The stable
   * report identity survives approved resumes; retaining never confirms a step. */
  async uploadArtifact(input: OperationDirectUploadInput): Promise<OperationArtifact> {
    const execution = this.requireExecution(), control = this.controls.get(execution);
    const guard = (): void => { this.requireExecution(execution); control?.throwIfStopped(); };
    guard();
    let upload = this.uploads.get(execution);
    if (!upload) {
      upload = operationUploader(execution.id, {
        guard, checkpoint: async () => { guard(); if (control) await control.checkpoint(); },
        aborted: () => control?.controller.signal.aborted ?? false,
        lookup: async declaration => this.runtimeRequest('artifact-upload-receipts', declaration) as Promise<WorkflowArtifactReceipt>,
        upload: async (declaration, bytes) => this.runtimeRequest('artifact-uploads', declaration, undefined, bytes) as Promise<WorkflowArtifactReceipt>,
      });
      this.uploads.set(execution, upload);
    }
    const artifact = await upload(input);
    guard();
    return artifact;
  }
  private requireExecution(expected?: Execution): Execution {
    const execution = this.request.getStore();
    if (!execution || expected && expected !== execution || this.finished.has(execution)) throw new Error('Workflow artifact authority requires the original trusted request');
    return execution;
  }
  private async runtimeRequest(path: 'artifact-receipts' | 'artifacts' | 'control' | 'artifact-upload-receipts' | 'artifact-uploads', report?: OperationArtifactReport | OperationArtifactUploadRequest, callerSignal?: AbortSignal, bytes?: Buffer): Promise<unknown> {
    const e = this.requireExecution(), control = this.controls.get(e);
    control?.throwIfStopped();
    const timeout = AbortSignal.timeout(this.timeout), inherited = callerSignal ?? control?.controller.signal;
    const signal = inherited ? AbortSignal.any([inherited, timeout]) : timeout;
    signal.throwIfAborted();
    const identity = await this.fetchImpl(this.identity, { signal, cache: 'no-store', redirect: 'error' });
    if (!identity.ok) throw new Error('Workflow operation workload identity unavailable');
    const token = await operationJSON(identity) as { access_token?: string };
    if (typeof token.access_token !== 'string' || !token.access_token || token.access_token.length > 8192) throw new Error('Invalid operation workload identity');
    signal.throwIfAborted(); this.requireExecution(e);
    const url = new URL(`/v1/runtime/workflow-operations/${e.id}/${path}`, this.api);
    if (bytes && report) {
      const declaration = report as OperationArtifactUploadRequest;
      url.search = new URLSearchParams({report_id: declaration.report_id, name: declaration.name, size_bytes: String(declaration.size_bytes), sha256: declaration.sha256}).toString();
    }
    const response = await this.fetchImpl(url, { method: path === 'control' ? 'GET' : 'POST', signal, cache: 'no-store', redirect: 'error', credentials: 'omit',
      headers: { Authorization: `Bearer ${token.access_token}`, ...(report ? { 'Content-Type': bytes ? 'application/octet-stream' : 'application/json' } : {}), 'X-Gregale-Operation-Execution-Kind': 'workflow',
        'X-Gregale-Operation-Workflow-Run-Id': e.runID, 'X-Gregale-Operation-Workflow-Step': e.step, 'X-Gregale-Operation-Generation': String(e.generation),
        'X-Gregale-Operation-Attempt': String(e.attempt), 'X-Gregale-Operation-Workflow-Capability': e.capability }, ...(report ? { body: bytes ?? JSON.stringify(report) } : {}) });
    const value = await operationResponse<unknown>(response);
    signal.throwIfAborted(); this.requireExecution(e); control?.throwIfStopped();
    return value;
  }
  private async artifactRequest(path: 'artifact-receipts' | 'artifacts', report: OperationArtifactReport): Promise<WorkflowArtifactReceipt> {
    const receipt = await this.runtimeRequest(path, report) as WorkflowArtifactReceipt;
    if (typeof receipt?.available !== 'boolean' || receipt.available !== !!receipt.artifact) throw new Error('Invalid workflow artifact receipt');
    const a = receipt.artifact;
    if (a && (!uuid.test(a.id) || a.name !== report.name || a.uri !== report.uri || a.size_bytes !== report.size_bytes || a.sha256 !== report.sha256)) throw new Error('Workflow artifact receipt declaration changed');
    return receipt;
  }
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
function workflowExecution(h: Headers, id: string): Execution {
  const runID = h.get('X-Gregale-Operation-Workflow-Run-Id') ?? '', step = h.get('X-Gregale-Operation-Workflow-Step') ?? '';
  const capability = h.get('X-Gregale-Operation-Workflow-Capability') ?? '';
  const generation = h.get('X-Gregale-Operation-Generation') ?? '', attempt = h.get('X-Gregale-Operation-Attempt') ?? '';
  const positive = (s: string): boolean => /^[1-9][0-9]*$/.test(s) && Number.isSafeInteger(Number(s));
  const invalid = (): never => { throw new Error('Invalid workflow operation context'); };
  if (h.get('X-Gregale-Operation-Execution-Kind') !== 'workflow' || !uuid.test(id) || !uuid.test(runID) || !uuid.test(capability) || !positive(generation) || !positive(attempt) || !/^[a-zA-Z0-9_-]{1,128}$/.test(step) || h.has('X-Faas-Invocation-Id')) invalid();
  h.forEach((_value, name) => { if (name.startsWith('x-gregale-operation-') && !['x-gregale-operation-execution-kind', 'x-gregale-operation-workflow-run-id', 'x-gregale-operation-workflow-step', 'x-gregale-operation-generation', 'x-gregale-operation-attempt', 'x-gregale-operation-workflow-capability'].includes(name)) invalid(); });
  for (const [name, value] of [['X-Faas-Workflow-Run-Id', runID], ['X-Faas-Workflow-Step', step], ['X-Faas-Workflow-Attempt', attempt]]) { if (h.has(name!) && h.get(name!) !== value) invalid(); }
  return { id, runID, step, capability, generation: Number(generation), attempt: Number(attempt) };
}
