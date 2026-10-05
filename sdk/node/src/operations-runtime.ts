import { AsyncLocalStorage } from 'node:async_hooks';
import { randomUUID } from 'node:crypto';
import { operationAPIBase, operationResponse, operationJSON, type Operation, type OperationReport, type OperationArtifactReport } from './operations.js';

export interface OperationExecutionContext { id: string; attempt: number; capability: string; invocationID: string }
export interface GregaleOperationsOptions { apiURL: string; identityEndpoint?: string; fetch?: typeof globalThis.fetch; timeoutMs?: number }
export type OperationRequestHeaders = HeadersInit | Record<string, string | string[] | undefined>;

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
    const normalized = headers instanceof Headers || Array.isArray(headers) ? new Headers(headers) : new Headers(Object.entries(headers).filter((entry): entry is [string, string | string[]] => entry[1] !== undefined).map(([key, value]) => [key, Array.isArray(value) ? value.join(', ') : value]));
    const id = normalized.get('X-Gregale-Customer-Operation-Id');
    if (!id) return this.request.run(undefined, handler);
    const context = operationExecutionContext(normalized, id);
    return this.request.run(context, handler);
  }
  async progress(report: Omit<OperationReport, 'report_id'> & { report_id?: string }): Promise<Operation> {
    return this.report('progress', { ...report, report_id: report.report_id ?? randomUUID() });
  }
  async artifact(artifact: Omit<OperationArtifactReport, 'report_id'> & { report_id?: string }): Promise<Operation> {
    return this.report('artifacts', { ...artifact, report_id: artifact.report_id ?? randomUUID() });
  }
  private async report(path: 'progress' | 'artifacts', body: OperationReport | OperationArtifactReport): Promise<Operation> {
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
    return operationResponse<Operation>(response);
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
