import { operationAPIBase, operationResponse, OperationHTTPError, type OperationReport, type OperationArtifact, type OperationArtifactReport } from './customer-operations.js';
import { OperationControlScope, OperationStoppedError, type OperationHandlerScope } from './operation-control.js';
import { prepareOperationArtifact, type OperationArtifactInput, type PreparedOperationArtifact } from './operation-artifact.js';
import type { OperationJobControlResponse } from './generated/models/OperationJobControlResponse.js';
import { operationUploader, type OperationDirectUploadInput } from './operation-upload.js';

export type JobOperationArtifactUploadInput = OperationDirectUploadInput;

export interface JobOperationContext { readonly accountID: string; readonly appID: string; readonly platformTenantID: string; readonly scope: string; readonly id: string; readonly runID: string; readonly generation: number; readonly attempt: number }
export interface JobOperationScope extends OperationHandlerScope {
  readonly operation: Readonly<JobOperationContext>;
  progress(report: OperationReport): Promise<void>;
  /** Files stay private until confirmed task exit. Requires a stable report ID. */
  prepareArtifact(input: OperationArtifactInput & { report_id: string }): PreparedOperationArtifact<OperationArtifact>;
  /** Upload bounded private bytes with the native task proof, without a bucket
   * writer or credential. Matching calls share one durable file receipt. */
  uploadArtifact(input: JobOperationArtifactUploadInput): Promise<OperationArtifact>;
}
export interface JobOperationOptions {
  apiURL: string;
  fetch?: typeof globalThis.fetch;
  timeoutMs?: number;
  /** The environment supplied to this single task by Gregale's scheduler. */
  env?: Readonly<NodeJS.ProcessEnv>;
}

/** Run business code once. A result receipt is prepared before normal task exit;
 * only the host's fenced exit receipt publishes business success. */
export async function runJobOperation<Input, Result>(options: JobOperationOptions, handler: (input: Input, scope: JobOperationScope) => Promise<Result>): Promise<Result> {
  const env = options.env ?? process.env;
  const id = env.GREGALE_CUSTOMER_OPERATION_ID, runID = env.GREGALE_RUN_ID, instanceID = env.GREGALE_CUSTOMER_OPERATION_JOB_INSTANCE_ID;
  const accountID = env.GREGALE_CUSTOMER_OPERATION_ACCOUNT_ID, appID = env.GREGALE_CUSTOMER_OPERATION_APP_ID;
  const platformTenantID = env.GREGALE_CUSTOMER_OPERATION_PLATFORM_TENANT_ID, scope = env.GREGALE_CUSTOMER_OPERATION_SCOPE;
  const generation = Number(env.GREGALE_CUSTOMER_OPERATION_GENERATION), attempt = Number(env.GREGALE_TASK_ATTEMPT);
  const capability = env.GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY;
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
  if (!accountID || !appID || !platformTenantID || !scope || !uuid.test(accountID) || !uuid.test(appID) || !uuid.test(platformTenantID) ||
      !id || !runID || !instanceID || !uuid.test(id) || !uuid.test(runID) || !uuid.test(instanceID) ||
      !Number.isSafeInteger(generation) || generation < 1 || attempt !== 1 || !capability || !/^[0-9a-f]{64}$/.test(capability) ||
      env.GREGALE_TASK_INDEX !== '0') throw new Error('Missing trusted Job Operation context');
  const rawInput = env.GREGALE_CUSTOMER_OPERATION_INPUT;
  if (!rawInput || Buffer.byteLength(rawInput) > 32 * 1024) throw new Error('Invalid Job Operation input');
  const input = JSON.parse(rawInput) as Input;
  const base = operationAPIBase(options.apiURL), fetchImpl = options.fetch ?? globalThis.fetch;
  const timeout = options.timeoutMs ?? 5000;
  if (!Number.isFinite(timeout) || timeout <= 0 || timeout > 10000) throw new Error('Invalid Job Operation timeout');
  const operation = Object.freeze({ accountID, appID, platformTenantID, scope, id, runID, generation, attempt });
  const headers = { 'X-Gregale-Operation-Job-Run-Id': runID, 'X-Gregale-Operation-Job-Instance-Id': instanceID,
    'X-Gregale-Operation-Generation': String(generation), 'X-Gregale-Operation-Attempt': String(attempt), 'X-Gregale-Operation-Job-Capability': capability };
  const request = async <T>(path: string, body: unknown, signal: AbortSignal): Promise<T> => {
    const response = await fetchImpl(new URL(`v1/runtime/job-operations/${id}/${path}`, base.href.endsWith('/') ? base : `${base.href}/`), {
      method: body === undefined ? 'GET' : 'POST', headers: body === undefined ? headers : { ...headers, 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body), redirect: 'error', credentials: 'omit', signal: AbortSignal.any([signal, AbortSignal.timeout(timeout)]),
    });
    return operationResponse<T>(response);
  };
  const control = new OperationControlScope(async signal => {
    const value = await request<OperationJobControlResponse>('control', undefined, signal);
    const validDate = (text: string): boolean => typeof text === 'string' && /^\d{4}-\d{2}-\d{2}T.*(?:Z|[+-]\d{2}:\d{2})$/.test(text) && Number.isFinite(Date.parse(text));
    if (value.account_id !== accountID || value.app_id !== appID || value.platform_tenant_id !== platformTenantID || value.scope !== scope ||
        value.operation_id !== id || value.job_run_id !== runID || value.generation !== generation || value.attempt !== attempt ||
        typeof value.cancellation_requested !== 'boolean' || !validDate(value.deadline_at) || !validDate(value.lease_expires_at) ||
        Date.parse(value.lease_expires_at) > Date.parse(value.deadline_at) || !validDate(value.observed_at) ||
        !Number.isInteger(value.poll_after_ms) || value.poll_after_ms < 100 || value.poll_after_ms > 1000) throw new OperationStoppedError('execution_authority_lost');
    return value;
  }, () => {});
  return control.run(async scope => {
    const report = async <T>(kind: string, body: unknown): Promise<T> => {
      // Retry a lost report acknowledgement with the same report ID. The
      // handler itself is never retried, and authority errors stop immediately.
      for (let n = 0; n < 3; n++) {
        scope.throwIfStopped();
        try { const value = await request<T>(kind, body, scope.signal); scope.throwIfStopped(); return value; }
        catch (error) { if (scope.signal.aborted || n === 2 || error instanceof OperationHTTPError && error.status < 500) throw error; }
      }
      throw new Error('Job Operation report attempts exhausted');
    };
    const artifactRequest = async (kind: string, declaration: OperationArtifactReport): Promise<OperationArtifact | undefined> => {
      await scope.checkpoint();
      const receipt = await report<{ available: boolean; artifact?: OperationArtifact }>(kind, declaration);
      if (typeof receipt?.available !== 'boolean' || receipt.available !== !!receipt.artifact) throw new Error('Invalid Job artifact receipt');
      const a = receipt.artifact;
      if (!a) return undefined;
      if (!uuid.test(a.id) || a.name !== declaration.name || a.uri !== declaration.uri || a.size_bytes !== declaration.size_bytes || a.sha256 !== declaration.sha256) throw new Error('Job artifact receipt declaration changed');
      return Object.freeze({ id: a.id, name: a.name, uri: a.uri, size_bytes: a.size_bytes, sha256: a.sha256, ...(a.expires_at ? { expires_at: a.expires_at } : {}) });
    };
    const uploadArtifact = operationUploader(id, {
      guard: scope.throwIfStopped, checkpoint: scope.checkpoint, aborted: () => scope.signal.aborted,
      lookup: declaration => request('artifact-upload-receipts', declaration, scope.signal),
      upload: async (declaration, bytes) => {
        const url = new URL(`v1/runtime/job-operations/${id}/artifact-uploads`, base.href.endsWith('/') ? base : `${base.href}/`);
        url.search = new URLSearchParams({ report_id: declaration.report_id, name: declaration.name, size_bytes: String(declaration.size_bytes), sha256: declaration.sha256 }).toString();
        const response = await fetchImpl(url, { method: 'POST', headers: { ...headers, 'Content-Type': 'application/octet-stream' }, body: bytes,
          redirect: 'error', credentials: 'omit', signal: AbortSignal.any([scope.signal, AbortSignal.timeout(timeout)]) });
        return operationResponse(response);
      },
    });
    const jobScope: JobOperationScope = Object.freeze({ ...scope, operation, uploadArtifact,
      progress: async (progress: OperationReport) => { await report('progress', { report_id: progress.report_id, progress }); },
      prepareArtifact: (input: OperationArtifactInput & { report_id: string }) => {
        scope.throwIfStopped();
        if (!input.report_id) throw new Error('Job artifacts require a stable report ID');
        return prepareOperationArtifact(input, scope.throwIfStopped, async declaration => {
          const artifact = await artifactRequest('artifacts', declaration);
          if (!artifact) throw new Error('Verified Job artifact receipt required');
          return artifact;
        }, declaration => artifactRequest('artifact-receipts', declaration), () => scope.signal);
      },
    });
    const result = await handler(input, jobScope);
    await scope.checkpoint();
    await report('result', { report_id: 'job-result', result });
    return result;
  });
}
