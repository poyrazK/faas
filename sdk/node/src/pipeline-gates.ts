/** Durable application decisions; native events are wakeups, never authority. */
import { JobContract, jobSnapshot, jobValueHash, jobScope } from './job-runtime.js';
import { pipelineName, pipelineScope, type PipelineQuery, type PipelineScope } from './pipeline-state.js';
import { NonRetryableError, type WorkflowContext } from './workflows.js';
import { fileEventHash, fileEventUUID } from './file-events.js';
import { createJobPipelineClient } from './job-pipeline-client.js';
import { createClient } from './isolated-client-preview.js';
import type { PipelineGateActor, PipelineGateChoice, PipelineGateIntent, PipelineGateKind, PipelineGateOutcome, PipelineGateRecord, PipelineGateView } from './pipeline-gate-types.js';
export type * from './pipeline-gate-types.js';

export class PipelineGateError extends NonRetryableError {
  constructor(readonly code: 'invalid_input' | 'conflict' | 'forbidden' | 'inactive' | 'pending' | 'blocked' | 'wake_limit' | 'request_failed') { super('pipeline-gate:' + code); }
}
export interface PipelineGateOptions { readonly timeoutMs: number; readonly requiredRoles: readonly string[]; }
export interface PipelineGateStore {
  open(intent: PipelineGateIntent, signal: AbortSignal): Promise<PipelineGateRecord>;
  /** Atomically expire using the database clock, then read. */
  resolve(id: string, signal: AbortSignal): Promise<PipelineGateRecord | null>;
  /** First decision wins; expiry and decision are one atomic statement. */
  decide(id: string, binding: string, proposal: { state: PipelineGateChoice; actor_id: string | null; payload: unknown }, signal: AbortSignal): Promise<PipelineGateRecord>;
  mark(id: string, decision: PipelineGateOutcome<unknown>, delivery: 'delivered' | 'closed', signal: AbortSignal): Promise<void>;
  pending(limit: number, scope: PipelineScope, pipeline: string, contract: string, signal: AbortSignal): Promise<readonly PipelineGateRecord[]>;
  list(runId: string, ownerId: string, signal: AbortSignal): Promise<readonly PipelineGateRecord[]>;
}
export function pipelineGateIntent<R>(value: Omit<PipelineGateIntent<R>, 'version' | 'id' | 'review_sha256' | 'binding_sha256'>): PipelineGateIntent<R> {
  if (Object.keys(value).sort().join() !== 'contract_sha256,input_sha256,kind,name,owner_id,pipeline_sha256,required_roles,review,scope,timeout_ms,workflow_run_id' || !['approval', 'input'].includes(value.kind) || !fileEventUUID(value.workflow_run_id) || !pipelineName(value.owner_id) || !pipelineName(value.name) || ![value.pipeline_sha256, value.input_sha256, value.contract_sha256].every(fileEventHash) || !Number.isSafeInteger(value.timeout_ms) || value.timeout_ms < 1 || value.timeout_ms > 86400000 || !Array.isArray(value.required_roles) || !value.required_roles.length || value.required_roles.length > 16 || value.required_roles.some(r => !pipelineName(r)) || Buffer.byteLength(JSON.stringify(value.review)) > 16384) throw new PipelineGateError('invalid_input');
  const scope = pipelineScope(value.scope), roles = [...new Set(value.required_roles)].sort();
  const base = { ...value, scope, required_roles: roles, version: 1 as const, id: jobValueHash(['pipeline-gate', scope, value.workflow_run_id, value.name]), review_sha256: jobValueHash(value.review) };
  return jobSnapshot({ ...base, binding_sha256: jobValueHash(base) });
}
export function parsePipelineGateRecord(value: unknown): PipelineGateRecord {
  try {
    const r = value as PipelineGateRecord, i = r.intent;
    const { version: _v, id: _id, review_sha256: _r, binding_sha256: _b, ...base } = i;
    if (Object.keys(r).sort().join() !== 'decision,delivery,expires_at,intent' || typeof r.expires_at !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(r.expires_at) || jobValueHash(pipelineGateIntent(base)) !== jobValueHash(i) || !Number.isFinite(Date.parse(r.expires_at)) || !['pending', 'delivered', 'closed'].includes(r.delivery)) throw new Error();
    const d = r.decision;
    if (d !== null && (!d || typeof d !== 'object' || Array.isArray(d)) || !d && r.delivery !== 'pending') throw new Error();
    if (d && (Object.keys(d).sort().join() !== 'actor_id,decided_at,payload,state' || !['approved', 'rejected', 'submitted', 'expired', 'cancelled'].includes(d.state) || typeof d.decided_at !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(d.decided_at) || !Number.isFinite(Date.parse(d.decided_at)) || d.actor_id !== null && !pipelineName(d.actor_id) || ['approved', 'rejected', 'submitted'].includes(d.state) && (!d.actor_id || (i.kind === 'approval' ? d.state === 'submitted' : d.state !== 'submitted')) || ['expired', 'cancelled'].includes(d.state) && d.payload !== null || d.state === 'expired' && d.actor_id !== null || Buffer.byteLength(JSON.stringify(d.payload)) > 16384)) throw new Error();
    return jobSnapshot({ intent: i, expires_at: r.expires_at, decision: d, delivery: r.delivery });
  } catch { throw new PipelineGateError('conflict'); }
}
export const pipelineGateSchema = `CREATE TABLE IF NOT EXISTS public.gregale_pipeline_gates (
 id text PRIMARY KEY CHECK (id ~ '^[a-f0-9]{64}$'),
 intent jsonb NOT NULL CHECK (jsonb_typeof(intent)='object'),
 expires_at timestamptz NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT '-infinity',
 decision jsonb,
 delivery text NOT NULL DEFAULT 'pending' CHECK (delivery IN ('pending','delivered','closed'))
);
CREATE INDEX IF NOT EXISTS gregale_pipeline_gates_pending ON public.gregale_pipeline_gates(delivery,expires_at);
`;
// Wire dates are normalized by PostgreSQL for cross-language replay.
const dateSQL = (column: string) => `to_char(${column} AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')`;
const returning = `jsonb_build_object('intent',intent,'expires_at',${dateSQL('expires_at')},'decision',decision,'delivery',delivery) AS record`;
const expiry = `jsonb_build_object('state','expired','actor_id',NULL,'payload',NULL,'decided_at',${dateSQL('clock_timestamp()')})`;
/** Install schema explicitly. Every query is an acknowledged autocommit on the
 * primary; never join these writes to an ambient business transaction. */
export function postgresPipelineGateStore(query: PipelineQuery): PipelineGateStore {
  async function rows(sql: string, args: unknown[], signal: AbortSignal) {
    signal.throwIfAborted(); const result = await query(sql, args, signal); signal.throwIfAborted();
    return result.rows.map(r => parsePipelineGateRecord(r.record));
  }
  async function resolve(id: string, signal: AbortSignal) {
    const found = await rows(`UPDATE public.gregale_pipeline_gates SET decision=CASE WHEN decision IS NULL AND expires_at<=clock_timestamp() THEN ${expiry} ELSE decision END WHERE id=$1 RETURNING ${returning}`, [id], signal);
    if (found.length > 1) throw new PipelineGateError('conflict'); return found[0] ?? null;
  }
  return {
    resolve,
    async open(intent, signal) {
      const values = await rows(`INSERT INTO public.gregale_pipeline_gates(id,intent,expires_at) VALUES ($1,$2::jsonb,clock_timestamp()+($3::bigint*interval '1 millisecond')) ON CONFLICT (id) DO NOTHING RETURNING ${returning}`, [intent.id, JSON.stringify(intent), intent.timeout_ms], signal);
      const r = values[0] ?? await resolve(intent.id, signal);
      if (!r || jobValueHash(r.intent) !== jobValueHash(intent)) throw new PipelineGateError('conflict'); return r;
    },
    async decide(id, binding, proposal, signal) {
      const values = await rows(`UPDATE public.gregale_pipeline_gates SET decision=CASE WHEN expires_at<=clock_timestamp() THEN ${expiry} ELSE $3::jsonb || jsonb_build_object('decided_at',${dateSQL('clock_timestamp()')}) END WHERE id=$1 AND intent->>'binding_sha256'=$2 AND decision IS NULL RETURNING ${returning}`, [id, binding, JSON.stringify(proposal)], signal);
      const r = values[0] ?? await resolve(id, signal); if (!r || r.intent.binding_sha256 !== binding) throw new PipelineGateError('conflict'); return r;
    },
    async mark(id, decision, delivery, signal) {
      const values = await rows(`UPDATE public.gregale_pipeline_gates SET delivery=$3 WHERE id=$1 AND decision=$2::jsonb RETURNING ${returning}`, [id, JSON.stringify(decision), delivery], signal);
      if (values.length !== 1) throw new PipelineGateError('conflict');
    },
    pending: (limit, scope, pipeline, contract, signal) => {
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 100) throw new PipelineGateError('invalid_input');
      return rows(`UPDATE public.gregale_pipeline_gates SET checked_at=clock_timestamp() WHERE id IN (SELECT id FROM public.gregale_pipeline_gates WHERE delivery='pending' AND intent->'scope'=$2::jsonb AND intent->>'pipeline_sha256'=$3 AND intent->>'contract_sha256'=$4 ORDER BY (decision IS NULL AND expires_at>clock_timestamp()),checked_at,expires_at,id LIMIT $1 FOR UPDATE SKIP LOCKED) RETURNING ${returning}`, [limit, JSON.stringify(scope), pipeline, contract], signal);
    },
    list: (runId, owner, signal) => rows(`SELECT ${returning} FROM public.gregale_pipeline_gates WHERE intent->>'workflow_run_id'=$1 AND intent->>'owner_id'=$2 ORDER BY id LIMIT 100`, [runId, owner], signal),
  };
}

export interface PipelineGateTransport {
  /** Check current account, app, root contract, owner and input digest. */
  active(intent: PipelineGateIntent, signal: AbortSignal): Promise<boolean>;
  /** Retry with the same native key/payload until acknowledged. */
  publish(intent: PipelineGateIntent, signal: AbortSignal): Promise<void>;
}
/** Backend-only transport. No mutable process-global client or browser token. */
export function nativePipelineGateTransport<I, O>(options: { scope: PipelineScope; token: string; contract: JobContract<I, O>; timeoutMs: number; fetch?: typeof fetch }): PipelineGateTransport {
  options = Object.freeze({ ...options, scope: pipelineScope(options.scope) });
  const scope = options.scope, client = createClient(scope.base_url, { token: options.token, fetch: options.fetch });
  return {
    async active(i, signal) {
      if (jobValueHash(i.scope) !== jobValueHash(scope) || i.pipeline_sha256 !== options.contract.sha256) throw new PipelineGateError('forbidden');
      const pipeline = createJobPipelineClient({ ...options, baseURL: scope.base_url, ownerId: i.owner_id });
      const state = await pipeline.resume({ version: 1, run_id: i.workflow_run_id, scope, owner_id: i.owner_id, contract_sha256: i.pipeline_sha256, input_sha256: i.input_sha256 }).inspect({ timeoutMs: options.timeoutMs, signal });
      return !state.cancelled && ['pending', 'running', 'awaiting_event'].includes(state.status);
    },
    async publish(i, signal) {
      const ack = await client.workflows.injectWorkflowEvent({ id: i.workflow_run_id, idempotencyKey: 'pipeline-gate:' + i.id, requestBody: { event_name: 'pipeline-gate.' + i.id, payload: { version: 1, gate_id: i.id, binding_sha256: i.binding_sha256 } } }, { timeoutMs: options.timeoutMs, signal, retry: false });
      if (ack.status !== 'received' || ack.event_name !== 'pipeline-gate.' + i.id) throw new PipelineGateError('request_failed');
    },
  };
}
export function createPipelineGateService<R, D, A>(options: {
  scope: PipelineScope; pipelineSha256: string; contract: JobContract<R, D>; store: PipelineGateStore; transport: PipelineGateTransport;
  /** Derive identity/membership from trusted session state on EVERY request. */
  authorize(record: PipelineGateRecord, action: 'inspect' | 'decide', access: A, signal: AbortSignal): Promise<PipelineGateActor>;
}) {
  options = Object.freeze({ ...options, scope: pipelineScope(options.scope) });
  const scope = options.scope, { contract, store, transport } = options;
  if (!fileEventHash(options.pipelineSha256)) throw new PipelineGateError('invalid_input');
  function owned(raw: PipelineGateRecord): PipelineGateRecord<R, D> {
    const r = parsePipelineGateRecord(raw);
    if (jobValueHash(r.intent.scope) !== jobValueHash(scope) || r.intent.contract_sha256 !== contract.sha256 || r.intent.pipeline_sha256 !== options.pipelineSha256) throw new PipelineGateError('forbidden');
    contract.input(r.intent.review);
    if (r.decision && ['approved', 'rejected', 'submitted'].includes(r.decision.state)) contract.output(r.decision.payload);
    return r as PipelineGateRecord<R, D>;
  }
  async function deliver(r: PipelineGateRecord<R, D>, signal: AbortSignal) {
    if (!r.decision || r.delivery !== 'pending') return;
    const active = await transport.active(r.intent, signal); signal.throwIfAborted();
    if (!active) { await store.mark(r.intent.id, r.decision, 'closed', signal); return; }
    await transport.publish(r.intent, signal); signal.throwIfAborted();
    await store.mark(r.intent.id, r.decision, 'delivered', signal);
  }
  const view = (r: PipelineGateRecord<R, D>, actor?: PipelineGateActor): PipelineGateView<R, D> => jobSnapshot({ can_decide: !r.decision && !!actor && r.intent.required_roles.every(role => actor.roles.includes(role)), id: r.intent.id, run_id: r.intent.workflow_run_id, name: r.intent.name, kind: r.intent.kind, review: r.intent.review, binding_sha256: r.intent.binding_sha256, expires_at: r.expires_at, decision: r.decision, delivery: r.delivery });
  async function access(id: string, action: 'inspect' | 'decide', auth: A, signal: AbortSignal) {
    if (!fileEventHash(id)) throw new PipelineGateError('forbidden');
    const raw = await store.resolve(id, signal); if (!raw) throw new PipelineGateError('forbidden');
    const r = owned(raw), actor = await options.authorize(r, action, auth, signal); signal.throwIfAborted();
    if (!actor || !pipelineName(actor.actor_id) || actor.owner_id !== r.intent.owner_id || !Array.isArray(actor.roles) || action === 'decide' && !r.intent.required_roles.every(role => actor.roles.includes(role))) throw new PipelineGateError('forbidden');
    const active = await transport.active(r.intent, signal); signal.throwIfAborted();
    if (!active && action === 'decide' && !r.decision) throw new PipelineGateError('inactive');
    return { r, actor, active };
  }
  const execute = <T>(settings: { timeoutMs: number; signal?: AbortSignal }, fn: (signal: AbortSignal) => Promise<T>) => jobScope(settings.timeoutMs, settings.signal, async signal => {
    try { const value = await fn(signal); signal.throwIfAborted(); return value; }
    catch (e) { signal.throwIfAborted(); if (e instanceof PipelineGateError) throw e; throw new PipelineGateError('request_failed'); }
  });
  return Object.freeze({
    inspect: (id: string, auth: A, settings: { timeoutMs: number; signal?: AbortSignal }) => execute(settings, async signal => { const { r, actor, active } = await access(id, 'inspect', auth, signal); return view(r, active ? actor : undefined); }),
    decide: (id: string, request: { binding_sha256: string; state: 'approved' | 'rejected' | 'submitted' | 'cancelled'; payload?: D }, auth: A, settings: { timeoutMs: number; signal?: AbortSignal }) => execute(settings, async signal => {
      const { r, actor } = await access(id, 'decide', auth, signal);
      if (request.binding_sha256 !== r.intent.binding_sha256 || !['approved', 'rejected', 'submitted', 'cancelled'].includes(request.state) || request.state !== 'cancelled' && (r.intent.kind === 'approval' ? request.state === 'submitted' : request.state !== 'submitted')) throw new PipelineGateError('conflict');
      const proposal = { state: request.state, actor_id: actor.actor_id, payload: request.state === 'cancelled' ? null : contract.output(request.payload) };
      if (Buffer.byteLength(JSON.stringify(proposal.payload)) > 16384) throw new PipelineGateError('invalid_input');
      const saved = owned(await store.decide(id, request.binding_sha256, proposal, signal)); signal.throwIfAborted();
      const d = saved.decision;
      if (!d || d.state !== proposal.state || d.actor_id !== proposal.actor_id || jobValueHash(d.payload) !== jobValueHash(proposal.payload)) throw new PipelineGateError('conflict');
      // A durable decision survives uncertain event delivery. drain() recovers it.
      try { await deliver(saved, signal); } catch { signal.throwIfAborted(); }
      return view(owned((await store.resolve(id, signal))!));
    }),
    drain: (limit: number, settings: { timeoutMs: number; signal?: AbortSignal }) => execute(settings, async signal => {
      let delivered = 0;
      for (const raw of await store.pending(limit, scope, options.pipelineSha256, contract.sha256, signal)) {
        signal.throwIfAborted();
        let r = owned((await store.resolve(raw.intent.id, signal))!); signal.throwIfAborted();
        if (!r.decision) { const active = await transport.active(r.intent, signal); signal.throwIfAborted(); if (!active) r = owned(await store.decide(r.intent.id, r.intent.binding_sha256, { state: 'cancelled', actor_id: null, payload: null }, signal)); }
        if (r.decision) { await deliver(r, signal); delivered++; }
      }
      return delivered;
    }),
  });
}

/** Called by the pipeline runtime, not by application handlers directly. */
export async function runPipelineGate<R, D>(wf: WorkflowContext, store: PipelineGateStore, intent: PipelineGateIntent<R>, contract: JobContract<R, D>, timeoutMs: number, claim: () => void, budget: (value: unknown) => unknown): Promise<PipelineGateOutcome<D>> {
  const verify = (r: PipelineGateRecord) => {
    r = parsePipelineGateRecord(r);
    if (jobValueHash(r.intent) !== jobValueHash(intent)) throw new PipelineGateError('conflict');
    return r;
  };
  async function io<T>(work: (signal: AbortSignal) => Promise<T>): Promise<T> {
    return jobScope(timeoutMs, undefined, async signal => {
      try { const value = await work(signal); signal.throwIfAborted(); return value; }
      catch (error) { signal.throwIfAborted(); if (error instanceof PipelineGateError) throw error; throw new Error('pipeline-gate:request_failed'); }
    });
  }
  const retry = { retry: { maxAttempts: 3, backoff: 'exponential' as const } };
  claim();
  const opened = await wf.step('pipeline-gate/' + intent.name + '/open', () => io(async signal => {
    const r = verify(await store.open(intent, signal)); return { id: intent.id, binding_sha256: intent.binding_sha256, expires_at: r.expires_at };
  }), retry);
  budget(opened);
  if (opened.id !== intent.id || opened.binding_sha256 !== intent.binding_sha256) throw new PipelineGateError('conflict');
  for (let wake = 0; wake < 16; wake++) {
    claim();
    // Ignore event data. Only the authoritative CAS decision can release work.
    await wf.waitForEvent('pipeline-gate/' + intent.name + '/wait', { event: 'pipeline-gate.' + intent.id, timeout: intent.timeout_ms });
    claim();
    const decision = await wf.step('pipeline-gate/' + intent.name + '/resolve', () => io(async signal => {
      const raw = await store.resolve(intent.id, signal); if (!raw) throw new PipelineGateError('conflict'); return verify(raw).decision;
    }), retry);
    budget(decision);
    if (decision) {
      const checked = verify({ intent, expires_at: opened.expires_at, decision, delivery: 'pending' }).decision!;
      if (['approved', 'rejected', 'submitted'].includes(checked.state)) contract.output(checked.payload);
      return jobSnapshot(checked) as PipelineGateOutcome<D>;
    }
  }
  throw new PipelineGateError('wake_limit');
}
