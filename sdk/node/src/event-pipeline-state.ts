/** Portable, application-owned event-to-workflow start receipts. */
import { jobSnapshot, jobValueHash } from './job-runtime.js';
import { eventConsumerUUID, newEventConsumerRequest } from './event-consumer-transactions.js';
import { pipelineName, pipelineScope, type PipelineScope, type PipelineQuery } from './pipeline-state.js';
import type { PipelineCheckpoint } from './job-pipeline-client.js';
import { fileEventHash, fileEventUUID } from './file-events.js';
import { eventContractType } from './event-contract.js';

export type EventPipelineCode = 'invalid_event' | 'unauthorized' | 'scope_mismatch' | 'receipt_conflict' | 'store_failed' | 'request_failed' | 'deadline' | 'cancelled';
export class EventPipelineError extends Error {
  constructor(readonly code: EventPipelineCode) { super('SDK event pipeline stopped: ' + code); this.name = 'EventPipelineError'; }
}
export interface EventPipelineIntent {
  readonly version: 1;
  readonly identity: string;
  readonly scope: PipelineScope;
  readonly name: string;
  readonly subscription_id: string;
  /** Canonical positive decimal int64, never a JS number. */
  readonly acceptance_id: string;
  readonly source: string;
  readonly event_id: string;
  readonly event_type: string;
  readonly body_sha256: string;
  readonly event_contract_sha256: string;
  readonly pipeline_sha256: string;
  readonly owner_id: string;
  readonly input_sha256: string;
  readonly idempotency_key: string;
}
export interface EventPipelineReceipt { readonly intent: EventPipelineIntent; readonly checkpoint: PipelineCheckpoint | null; }
export interface EventPipelineStore {
  load(identity: string, signal: AbortSignal): Promise<EventPipelineReceipt | null>;
  /** Only an acknowledged created=true may authorize the initial start. */
  reserve(intent: EventPipelineIntent, signal: AbortSignal): Promise<{ created: boolean; receipt: EventPipelineReceipt }>;
  complete(intent: EventPipelineIntent, checkpoint: PipelineCheckpoint, signal: AbortSignal): Promise<EventPipelineReceipt>;
}
export function eventPipelineScope(value: PipelineScope): PipelineScope {
  try { return pipelineScope({ ...value, account_id: eventConsumerUUID(value.account_id), app_id: eventConsumerUUID(value.app_id) }); }
  catch { throw new EventPipelineError('scope_mismatch'); }
}
function acceptance(value: unknown): value is string {
  return typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= 9223372036854775807n;
}
export function eventPipelineIdentity(scope: PipelineScope, name: string, subscriptionId: string, acceptanceId: string): string {
  try {
    if (!pipelineName(name) || !acceptance(acceptanceId)) throw new Error();
    return jobValueHash(['gregale.event-pipeline.v1', eventPipelineScope(scope), name, eventConsumerUUID(subscriptionId), acceptanceId]);
  } catch { throw new EventPipelineError('invalid_event'); }
}
export function matchEventPipelineCheckpoint(intent: EventPipelineIntent, value: unknown): PipelineCheckpoint {
  try {
    const c = value as PipelineCheckpoint;
    if (!c || Object.keys(c).sort().join() !== 'contract_sha256,input_sha256,owner_id,run_id,scope,version'
      || c.version !== 1 || !fileEventUUID(c.run_id) || c.owner_id !== intent.owner_id || c.contract_sha256 !== intent.pipeline_sha256
      || c.input_sha256 !== intent.input_sha256 || jobValueHash(c.scope) !== jobValueHash(intent.scope)) throw new Error();
    return jobSnapshot(c);
  } catch { throw new EventPipelineError('receipt_conflict'); }
}
export function parseEventPipelineReceipt(value: unknown): EventPipelineReceipt {
  try {
    const r = value as EventPipelineReceipt, i = r?.intent;
    if (!i || Object.keys(r).sort().join() !== 'checkpoint,intent'
      || Object.keys(i).sort().join() !== 'acceptance_id,body_sha256,event_contract_sha256,event_id,event_type,idempotency_key,identity,input_sha256,name,owner_id,pipeline_sha256,scope,source,subscription_id,version'
      || i.version !== 1 || !pipelineName(i.owner_id)
      || ![i.body_sha256, i.event_contract_sha256, i.pipeline_sha256, i.input_sha256].every(fileEventHash)
      || !eventContractType(i.event_type)
      || i.subscription_id !== eventConsumerUUID(i.subscription_id)
      || jobValueHash(i.scope) !== jobValueHash(eventPipelineScope(i.scope))
      || i.identity !== eventPipelineIdentity(i.scope, i.name, i.subscription_id, i.acceptance_id)
      || i.idempotency_key !== 'event-pipeline:' + i.identity) throw new Error();
    newEventConsumerRequest({ accountId: i.scope.account_id, appId: i.scope.app_id, subscriptionId: i.subscription_id,
      acceptanceId: i.acceptance_id, source: i.source, eventId: i.event_id },
    Buffer.from(JSON.stringify({ specversion: '1.0', source: i.source, id: i.event_id, type: i.event_type, data: null })));
    if (r.checkpoint !== null) matchEventPipelineCheckpoint(i, r.checkpoint);
    return jobSnapshot(r);
  } catch { throw new EventPipelineError('receipt_conflict'); }
}
export const eventPipelineSchema = `CREATE TABLE IF NOT EXISTS public.gregale_event_pipeline_starts (
 identity text PRIMARY KEY CHECK (identity ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL CHECK (jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
`;
/** Each query is one acknowledged autocommit statement on the same primary.
 * No ambient transaction, read replica, automatic DDL, pruning or lease takeover. */
export function postgresEventPipelineStore(query: PipelineQuery): EventPipelineStore {
  const load = async (identity: string, signal: AbortSignal): Promise<EventPipelineReceipt | null> => {
    signal.throwIfAborted();
    const r = await query('SELECT receipt FROM public.gregale_event_pipeline_starts WHERE identity=$1', [identity], signal);
    signal.throwIfAborted();
    if (r.rows.length > 1) throw new EventPipelineError('receipt_conflict');
    return r.rows.length ? parseEventPipelineReceipt(r.rows[0]!.receipt) : null;
  };
  return Object.freeze({ load,
    async reserve(intent, signal) {
      signal.throwIfAborted(); parseEventPipelineReceipt({ intent, checkpoint: null });
      const r = await query('INSERT INTO public.gregale_event_pipeline_starts(identity,receipt) VALUES ($1,$2::jsonb) ON CONFLICT (identity) DO NOTHING RETURNING receipt', [intent.identity, JSON.stringify({ intent, checkpoint: null })], signal);
      signal.throwIfAborted();
      const receipt = r.rows.length === 1 ? parseEventPipelineReceipt(r.rows[0]!.receipt) : await load(intent.identity, signal);
      if (r.rows.length > 1 || !receipt || jobValueHash(receipt.intent) !== jobValueHash(intent)) throw new EventPipelineError('receipt_conflict');
      return { created: r.rows.length === 1, receipt };
    },
    async complete(intent, checkpoint, signal) {
      signal.throwIfAborted(); matchEventPipelineCheckpoint(intent, checkpoint); parseEventPipelineReceipt({ intent, checkpoint });
      const r = await query("UPDATE public.gregale_event_pipeline_starts SET receipt=jsonb_set(receipt,'{checkpoint}',$3::jsonb) WHERE identity=$1 AND receipt->'intent'=$2::jsonb AND (receipt->'checkpoint'='null'::jsonb OR receipt->'checkpoint'=$3::jsonb) RETURNING receipt", [intent.identity, JSON.stringify(intent), JSON.stringify(checkpoint)], signal);
      signal.throwIfAborted();
      if (r.rows.length !== 1) throw new EventPipelineError('receipt_conflict');
      const receipt = parseEventPipelineReceipt(r.rows[0]!.receipt);
      if (jobValueHash(receipt.intent) !== jobValueHash(intent) || jobValueHash(receipt.checkpoint) !== jobValueHash(checkpoint)) throw new EventPipelineError('receipt_conflict');
      return receipt;
    },
  } satisfies EventPipelineStore);
}
