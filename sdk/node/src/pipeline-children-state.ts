import { jobSnapshot, jobValueHash, jobInputId } from './job-runtime.js';
import { pipelineScope, pipelineName, type PipelineScope, type PipelineQuery } from './pipeline-state.js';
import { fileEventHash, fileEventUUID } from './file-events.js';
import { NonRetryableError } from './workflows.js';
import type { PipelineCheckpoint } from './job-pipeline-client.js';
import type { PipelineChildPhase, PipelineBatchStop } from './pipeline-children-types.js';

export class PipelineChildrenError extends NonRetryableError {
  constructor(readonly code: 'invalid_input' | 'conflict' | 'forbidden' | 'contention' | 'poll_limit' | 'request_failed' | 'blocked') { super('pipeline-children:' + code); }
}
export interface PipelineChildrenIntent {
  readonly version: 1; readonly identity: string; readonly scope: PipelineScope;
  readonly parent_run_id: string; readonly owner_id: string; readonly batch: string;
  readonly parent_sha256: string; readonly parent_input_sha256: string;
  readonly child_sha256: string; readonly child_workflow: string;
  readonly inputs: readonly { readonly id: string; readonly value: unknown; readonly input_sha256: string }[];
  readonly concurrency: number; readonly timeout_ms: number;
  readonly failure_policy: 'collect' | 'fail_fast';
  readonly on_fail_fast: 'leave_running' | 'cancel_children';
  readonly on_deadline: 'leave_running' | 'cancel_children';
  readonly on_parent_stopped: 'leave_running' | 'cancel_children';
}
export interface PipelineChildState {
  readonly phase: PipelineChildPhase; readonly checkpoint: PipelineCheckpoint | null;
  readonly cancel_requested: boolean; readonly output?: unknown; readonly native_status?: string;
}
export interface PipelineChildrenState {
  readonly stop_reason: PipelineBatchStop; readonly parent_stopped: boolean;
  readonly children: readonly PipelineChildState[];
}
export interface PipelineChildrenReceipt {
  readonly intent: PipelineChildrenIntent; readonly state: PipelineChildrenState;
  readonly revision: number; readonly expires_at: string; readonly expired: boolean;
}
export function pipelineChildrenIdentity(scope: PipelineScope, runId: string, batch: string): string {
  if (!fileEventUUID(runId) || !pipelineName(batch)) throw new PipelineChildrenError('invalid_input');
  return jobValueHash(['pipeline-children', pipelineScope(scope), runId, batch]);
}
export function pipelineChildLineage(i: Pick<PipelineChildrenIntent, 'identity' | 'parent_run_id' | 'inputs'>, n: number) {
  return { version: 1 as const, parent_run_id: i.parent_run_id, batch_identity: i.identity, item_id: i.inputs[n]!.id };
}
export function pipelineChildrenIntent(value: Omit<PipelineChildrenIntent, 'version' | 'identity'>): PipelineChildrenIntent {
  const scope = pipelineScope(value.scope);
  if (!pipelineName(value.owner_id) || !/^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$/.test(value.child_workflow) || ![value.parent_sha256, value.parent_input_sha256, value.child_sha256].every(fileEventHash) || !Array.isArray(value.inputs) || !value.inputs.length || value.inputs.length > 50 || new Set(value.inputs.map(i => i.id)).size !== value.inputs.length || value.inputs.some(i => !jobInputId(i.id) || i.input_sha256 !== jobValueHash({ lineage: { version: 1, parent_run_id: value.parent_run_id, batch_identity: pipelineChildrenIdentity(scope, value.parent_run_id, value.batch), item_id: i.id }, version: 1, scope, owner_id: value.owner_id, contract_sha256: value.child_sha256, input: i.value })) || !Number.isSafeInteger(value.concurrency) || value.concurrency < 1 || value.concurrency > 10 || !Number.isSafeInteger(value.timeout_ms) || value.timeout_ms < 1 || value.timeout_ms > 86400000 || !['collect', 'fail_fast'].includes(value.failure_policy) || ![value.on_fail_fast, value.on_parent_stopped, value.on_deadline].every(p => ['leave_running', 'cancel_children'].includes(p)) || Buffer.byteLength(JSON.stringify(value)) > 65536) throw new PipelineChildrenError('invalid_input');
  return jobSnapshot({ ...value, scope, version: 1, identity: pipelineChildrenIdentity(scope, value.parent_run_id, value.batch) });
}
export function matchPipelineChild(i: PipelineChildrenIntent, index: number, c: PipelineCheckpoint): PipelineCheckpoint {
  if (!c || Object.keys(c).sort().join() !== 'contract_sha256,input_sha256,owner_id,run_id,scope,version' || c.version !== 1 || !fileEventUUID(c.run_id) || c.run_id === i.parent_run_id || jobValueHash(c.scope) !== jobValueHash(i.scope) || c.owner_id !== i.owner_id || c.contract_sha256 !== i.child_sha256 || c.input_sha256 !== i.inputs[index]?.input_sha256) throw new PipelineChildrenError('conflict');
  return jobSnapshot(c);
}
export function parsePipelineChildrenReceipt(value: unknown): PipelineChildrenReceipt {
  try {
    const r = value as PipelineChildrenReceipt, { version: _v, identity: _id, ...base } = r.intent;
    if (Object.keys(r).sort().join() !== 'expired,expires_at,intent,revision,state' || jobValueHash(pipelineChildrenIntent(base)) !== jobValueHash(r.intent) || !Number.isSafeInteger(r.revision) || r.revision < 0 || typeof r.expired !== 'boolean' || !Number.isFinite(Date.parse(r.expires_at)) || Object.keys(r.state).sort().join() !== 'children,parent_stopped,stop_reason' || typeof r.state.parent_stopped !== 'boolean' || ![null, 'deadline', 'child_failed', 'parent_stopped'].includes(r.state.stop_reason) || !Array.isArray(r.state.children) || r.state.children.length !== r.intent.inputs.length || Buffer.byteLength(JSON.stringify(r.state)) > 131072) throw new Error();
    const ids = new Set<string>();
    r.state.children.forEach((c, n) => {
      if (!['unstarted', 'submission_unresolved', 'running', 'succeeded', 'failed', 'cancelled'].includes(c.phase) || typeof c.cancel_requested !== 'boolean' || ['unstarted', 'submission_unresolved'].includes(c.phase) !== (c.checkpoint === null) || c.phase !== 'succeeded' && Object.hasOwn(c, 'output') || c.phase === 'succeeded' && !Object.hasOwn(c, 'output') || c.native_status !== undefined && !['pending', 'running', 'awaiting_event', 'succeeded', 'failed', 'dead'].includes(c.native_status)) throw new Error();
      if (c.checkpoint) { matchPipelineChild(r.intent, n, c.checkpoint); if (ids.has(c.checkpoint.run_id)) throw new Error(); ids.add(c.checkpoint.run_id); }
    });
    if (r.state.children.filter(c => ['submission_unresolved', 'running'].includes(c.phase)).length > r.intent.concurrency) throw new Error();
    return jobSnapshot(r);
  } catch { throw new PipelineChildrenError('conflict'); }
}
export interface PipelineChildrenStore {
  open(intent: PipelineChildrenIntent, signal: AbortSignal): Promise<PipelineChildrenReceipt>;
  load(identity: string, signal: AbortSignal): Promise<PipelineChildrenReceipt | null>;
  /** CAS on the entire batch: concurrency and stop decisions share one revision.
   * Admission additionally checks the primary's clock in the same statement. */
  save(old: PipelineChildrenReceipt, state: PipelineChildrenState, admission: boolean, signal: AbortSignal): Promise<PipelineChildrenReceipt | null>;
  scan(scope: PipelineScope, parentSha256: string, limit: number, signal: AbortSignal): Promise<readonly PipelineChildrenReceipt[]>;
}
export const pipelineChildrenSchema = `CREATE TABLE IF NOT EXISTS public.gregale_pipeline_children (
 identity text PRIMARY KEY CHECK (identity ~ '^[a-f0-9]{64}$'),
 intent jsonb NOT NULL CHECK (jsonb_typeof(intent)='object'),
 state jsonb NOT NULL CHECK (jsonb_typeof(state)='object'),
 revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0),
 expires_at timestamptz NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT '-infinity'
);
`;
const fields = `jsonb_build_object('intent',intent,'state',state,'revision',revision,'expires_at',to_char(expires_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),'expired',expires_at<=clock_timestamp()) AS receipt`;
/** Explicit app migration; one acknowledged autocommit per query, same primary. */
export function postgresPipelineChildrenStore(query: PipelineQuery): PipelineChildrenStore {
  async function rows(sql: string, values: unknown[], signal: AbortSignal) {
    signal.throwIfAborted(); const r = await query(sql, values, signal); signal.throwIfAborted(); return r.rows.map(v => parsePipelineChildrenReceipt(v.receipt));
  }
  async function load(id: string, signal: AbortSignal) { const r = await rows(`SELECT ${fields} FROM public.gregale_pipeline_children WHERE identity=$1`, [id], signal); if (r.length > 1) throw new PipelineChildrenError('conflict'); return r[0] ?? null; }
  return { load,
    async open(intent, signal) {
      const state: PipelineChildrenState = { stop_reason: null, parent_stopped: false, children: intent.inputs.map(() => ({ phase: 'unstarted', checkpoint: null, cancel_requested: false })) };
      const r = await rows(`INSERT INTO public.gregale_pipeline_children(identity,intent,state,expires_at) VALUES ($1,$2::jsonb,$3::jsonb,clock_timestamp()+($4::bigint*interval '1 millisecond')) ON CONFLICT (identity) DO NOTHING RETURNING ${fields}`, [intent.identity, JSON.stringify(intent), JSON.stringify(state), intent.timeout_ms], signal);
      const saved = r[0] ?? await load(intent.identity, signal); if (!saved || jobValueHash(saved.intent) !== jobValueHash(intent)) throw new PipelineChildrenError('conflict'); return saved;
    },
    async save(old, state, admission, signal) {
      parsePipelineChildrenReceipt({ ...old, state, revision: old.revision + 1 });
      const r = await rows(`UPDATE public.gregale_pipeline_children SET state=$4::jsonb,revision=revision+1 WHERE identity=$1 AND revision=$2 AND intent=$3::jsonb AND (NOT $5::boolean OR expires_at>clock_timestamp()) RETURNING ${fields}`, [old.intent.identity, old.revision, JSON.stringify(old.intent), JSON.stringify(state), admission], signal);
      if (r.length > 1) throw new PipelineChildrenError('conflict'); return r[0] ?? null;
    },
    async scan(scope, parent, limit, signal) {
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 100) throw new PipelineChildrenError('invalid_input');
      return rows(`UPDATE public.gregale_pipeline_children SET checked_at=clock_timestamp() WHERE identity IN (SELECT identity FROM public.gregale_pipeline_children WHERE intent->'scope'=$1::jsonb AND intent->>'parent_sha256'=$2 AND EXISTS (SELECT 1 FROM jsonb_array_elements(state->'children') c WHERE c->>'phase' IN ('submission_unresolved','running') OR c->>'phase'='unstarted' AND state->>'stop_reason' IS NULL) ORDER BY checked_at,identity LIMIT $3 FOR UPDATE SKIP LOCKED) RETURNING ${fields}`, [JSON.stringify(scope), parent, limit], signal);
    },
  };
}
