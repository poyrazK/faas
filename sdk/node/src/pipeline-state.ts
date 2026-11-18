/** Portable pipeline ownership, durable job receipts and verified file handoff. */
import { jobHash, jobValueHash, jobSnapshot, jobScope } from './job-runtime.js';
import { parseJobCheckpoint, type JobCheckpoint } from './typed-jobs.js';
import { NonRetryableError } from './workflows.js';
import { fileEventUUID, fileEventHash } from './file-events.js';
import type { FileBucket, FileOptions, FileScope } from './files.js';

export type PipelineCode = 'invalid_input' | 'scope_mismatch' | 'receipt_conflict' | 'store_failed' | 'submission_unresolved' | 'job_failed' | 'invalid_result' | 'poll_limit' | 'operation_limit' | 'journal_limit' | 'artifact_changed' | 'request_failed' | 'acceptance_unknown' | 'deadline' | 'cancelled';
export class PipelineError extends NonRetryableError {
  constructor(readonly code: PipelineCode, readonly step?: string, readonly jobRunId?: string) {
    super('pipeline:' + code + (step ? ':' + step : '') + (jobRunId ? ':' + jobRunId : ''));
  }
}
const pipelineCodes = new Set<string>(['invalid_input', 'scope_mismatch', 'receipt_conflict', 'store_failed', 'submission_unresolved', 'job_failed', 'invalid_result', 'poll_limit', 'operation_limit', 'journal_limit', 'artifact_changed', 'request_failed', 'acceptance_unknown', 'deadline', 'cancelled']);
/** Decode only SDK codes; never expose arbitrary native/database error text. */
export function pipelineFailure(value: unknown): { readonly code: PipelineCode; readonly jobRunId?: string } | undefined {
  if (typeof value !== 'string') return;
  const parts = value.split(':');
  if (parts[0] !== 'pipeline' || !pipelineCodes.has(parts[1] ?? '')) return;
  const last = parts.at(-1);
  return Object.freeze({ code: parts[1] as PipelineCode, ...(fileEventUUID(last) ? { jobRunId: last } : {}) });
}
export interface PipelineScope { readonly base_url: string; readonly account_id: string; readonly app_id: string; readonly app_slug: string; }
export interface PipelineLineage { readonly version: 1; readonly parent_run_id: string; readonly batch_identity: string; readonly item_id: string; }
export interface PipelineTrigger { readonly version: 1; readonly kind: 'event' | 'schedule' | 'tool'; readonly identity: string; }
export function parsePipelineTrigger(value: unknown): PipelineTrigger {
  const v = value as PipelineTrigger;
  if (!v || Object.keys(v).sort().join() !== 'identity,kind,version' || v.version !== 1 || !['event', 'schedule', 'tool'].includes(v.kind) || !fileEventHash(v.identity)) throw new PipelineError('receipt_conflict');
  return jobSnapshot(v);
}
export function parsePipelineLineage(value: unknown): PipelineLineage {
  const v = value as PipelineLineage;
  if (!v || Object.keys(v).sort().join() !== 'batch_identity,item_id,parent_run_id,version' || v.version !== 1 || !fileEventUUID(v.parent_run_id) || !fileEventHash(v.batch_identity) || typeof v.item_id !== 'string' || !/^[A-Za-z0-9_.:-]{1,128}$/.test(v.item_id)) throw new PipelineError('receipt_conflict');
  return jobSnapshot(v);
}
export interface PipelineEnvelope<I> {
  readonly lineage?: PipelineLineage;
  readonly trigger?: PipelineTrigger;
  readonly version: 1; readonly contract_sha256: string; readonly scope: PipelineScope; readonly owner_id: string; readonly input: I;
}
export interface PipelineResultEnvelope<O> extends Omit<PipelineEnvelope<O>, 'input'> { readonly output: O; }
export interface PipelineJobIntent {
  readonly version: 1; readonly identity: string; readonly scope: PipelineScope;
  readonly workflow_run_id: string; readonly step: string; readonly owner_id: string;
  readonly pipeline_sha256: string; readonly contract_sha256: string; readonly job_name: string;
  readonly values_sha256: string; readonly input_digest: string; readonly idempotency_key: string;
  readonly inputs: JobCheckpoint['inputs'];
}
export interface PipelineJobReceipt { readonly intent: PipelineJobIntent; readonly checkpoint: JobCheckpoint | null; }
export interface PipelineJobStore {
  load(identity: string, signal: AbortSignal): Promise<PipelineJobReceipt | null>;
  reserve(intent: PipelineJobIntent, signal: AbortSignal): Promise<{ created: boolean; receipt: PipelineJobReceipt }>;
  complete(intent: PipelineJobIntent, checkpoint: JobCheckpoint, signal: AbortSignal): Promise<PipelineJobReceipt>;
}
export function pipelineScope(value: PipelineScope): PipelineScope {
  try {
    const url = new URL(value.base_url);
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || url.href.replace(/\/+$/, '') !== value.base_url || !fileEventUUID(value.account_id) || !fileEventUUID(value.app_id) || !/^[a-z0-9][a-z0-9-]{0,62}$/.test(value.app_slug)) throw new Error();
    return jobSnapshot({ base_url: value.base_url, account_id: value.account_id, app_id: value.app_id, app_slug: value.app_slug });
  } catch { throw new PipelineError('invalid_input'); }
}
export const pipelineName = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(v);
export function pipelineJobIdentity(scope: PipelineScope, runId: string, step: string): string {
  if (!fileEventUUID(runId) || !pipelineName(step)) throw new PipelineError('invalid_input');
  return jobValueHash([pipelineScope(scope), runId, step]);
}
export function matchPipelineJob(intent: PipelineJobIntent, value: unknown): JobCheckpoint {
  const c = parseJobCheckpoint(value);
  if (c.base_url !== intent.scope.base_url || c.account_id !== intent.scope.account_id || c.job_name !== intent.job_name || c.contract_sha256 !== intent.contract_sha256 || c.input_digest !== intent.input_digest || c.source_run_id !== null || jobValueHash(c.inputs) !== jobValueHash(intent.inputs)) throw new PipelineError('receipt_conflict');
  return c;
}
export function parsePipelineJobReceipt(value: unknown): PipelineJobReceipt {
  try {
    const r = value as PipelineJobReceipt, i = r.intent;
    if (Object.keys(r).sort().join() !== 'checkpoint,intent' || Object.keys(i).sort().join() !== 'contract_sha256,idempotency_key,identity,input_digest,inputs,job_name,owner_id,pipeline_sha256,scope,step,values_sha256,version,workflow_run_id' || i.version !== 1 || i.identity !== pipelineJobIdentity(i.scope, i.workflow_run_id, i.step) || i.idempotency_key !== 'pipeline-job:' + i.identity || !pipelineName(i.owner_id) || ![i.pipeline_sha256, i.contract_sha256, i.values_sha256, i.input_digest].every(fileEventHash) || !/^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/.test(i.job_name) || !Array.isArray(i.inputs) || !i.inputs.length || i.inputs.length > 1000 || new Set(i.inputs.map(x => x.input_id)).size !== i.inputs.length || i.inputs.some(x => Object.keys(x).sort().join() !== 'binding_sha256,input_id,source_task_index' || !/^[A-Za-z0-9_.:-]{1,128}$/.test(x.input_id) || !fileEventHash(x.binding_sha256) || x.source_task_index !== null) || jobValueHash(i.scope) !== jobValueHash(pipelineScope(i.scope))) throw new Error();
    if (r.checkpoint !== null) matchPipelineJob(i, r.checkpoint);
    return jobSnapshot(r);
  } catch { throw new PipelineError('receipt_conflict'); }
}
export const pipelineJobSchema = `CREATE TABLE IF NOT EXISTS public.gregale_pipeline_jobs (
 identity text PRIMARY KEY CHECK (identity ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
`;
export type PipelineQuery = (sql: string, values: unknown[], signal: AbortSignal) => Promise<{ rows: Record<string, unknown>[] }>;
function logicalIntent(intent: PipelineJobIntent): string { return jobValueHash({ ...intent, input_digest: null, inputs: [] }); }
/** Each query must be one acknowledged autocommit statement, never part of an
 * ambient business transaction. Install the schema explicitly before serving. */
export function postgresPipelineJobStore(query: PipelineQuery): PipelineJobStore {
  const check = (signal: AbortSignal) => signal.throwIfAborted();
  const load = async (identity: string, signal: AbortSignal): Promise<PipelineJobReceipt | null> => {
    check(signal); const r = await query('SELECT receipt FROM public.gregale_pipeline_jobs WHERE identity=$1', [identity], signal); check(signal);
    if (r.rows.length > 1) throw new PipelineError('receipt_conflict');
    return r.rows.length ? parsePipelineJobReceipt(r.rows[0]!.receipt) : null;
  };
  return Object.freeze({ load, async reserve(intent, signal) {
    check(signal); const r = await query('INSERT INTO public.gregale_pipeline_jobs(identity,receipt) VALUES ($1,$2::jsonb) ON CONFLICT (identity) DO NOTHING RETURNING receipt', [intent.identity, JSON.stringify({ intent, checkpoint: null })], signal); check(signal);
    const receipt = r.rows.length === 1 ? parsePipelineJobReceipt(r.rows[0]!.receipt) : await load(intent.identity, signal);
    if (r.rows.length > 1 || !receipt || (r.rows.length === 1 ? jobValueHash(receipt.intent) !== jobValueHash(intent) : logicalIntent(receipt.intent) !== logicalIntent(intent))) throw new PipelineError('receipt_conflict');
    return { created: r.rows.length === 1, receipt };
  }, async complete(intent, checkpoint, signal) {
    check(signal); matchPipelineJob(intent, checkpoint);
    const r = await query("UPDATE public.gregale_pipeline_jobs SET receipt=jsonb_set(receipt,'{checkpoint}',$3::jsonb) WHERE identity=$1 AND receipt->'intent'=$2::jsonb AND (receipt->'checkpoint'='null'::jsonb OR receipt->'checkpoint'=$3::jsonb) RETURNING receipt", [intent.identity, JSON.stringify(intent), JSON.stringify(checkpoint)], signal); check(signal);
    if (r.rows.length !== 1) throw new PipelineError('receipt_conflict');
    return parsePipelineJobReceipt(r.rows[0]!.receipt);
  } } satisfies PipelineJobStore);
}

export interface PipelineArtifact {
  readonly version: 1; readonly scope: FileScope; readonly owner_id: string;
  readonly key: string; readonly version_id?: string | null; readonly size_bytes: number; readonly sha256: string;
}
export function parsePipelineArtifact(value: unknown, files: FileBucket, ownerId: string): PipelineArtifact {
  const a = value as PipelineArtifact;
  if (!a || a.version !== 1 || !pipelineName(ownerId) || a.owner_id !== ownerId || jobValueHash(a.scope) !== jobValueHash(files.scope) || typeof a.key !== 'string' || !a.key || Buffer.byteLength(a.key) > 1024 || /[\x00-\x1f]/.test(a.key) || !Number.isSafeInteger(a.size_bytes) || a.size_bytes < 0 || !fileEventHash(a.sha256) || a.version_id != null && !fileEventUUID(a.version_id)) throw new PipelineError('artifact_changed');
  return jobSnapshot({ version: 1, scope: files.scope, owner_id: ownerId, key: a.key, ...(a.version_id ? { version_id: a.version_id } : {}), size_bytes: a.size_bytes, sha256: a.sha256 });
}
/** No bearer URLs enter the workflow journal. Every read verifies bytes, even
 * when an immutable provider version is available. Authorize ownerId first. */
export function readPipelineArtifact(files: FileBucket, artifact: PipelineArtifact, ownerId: string, options: FileOptions): Promise<Uint8Array> {
  const a = parsePipelineArtifact(artifact, files, ownerId);
  if (a.size_bytes > (options.maxBytes ?? 16 * 1024 * 1024)) throw new PipelineError('journal_limit');
  const deadline = performance.now() + options.timeoutMs;
  return jobScope(options.timeoutMs, options.signal, async signal => {
    const bytes = await files.download(a.key, { ...options, signal, maxBytes: a.size_bytes || 1, ...(a.version_id ? { versionId: a.version_id } : {}) });
    signal.throwIfAborted();
    if (bytes.length !== a.size_bytes || jobHash(bytes) !== a.sha256) throw new PipelineError('artifact_changed');
    if (performance.now() >= deadline) throw new PipelineError('deadline');
    return bytes;
  });
}
