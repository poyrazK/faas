/** In-memory development simulator. Never a production storage backend. */
import { createHash } from 'node:crypto';
import type { DurableEntityRegistry } from './durable-entity-runtime.js';
import { decodeDurableEntityHandlerRequest, durableEntityJSON, encodeDurableEntityTransition,
  type DurableEntityIdentity, type DurableEntityHandlerLimits, type DurableEntityWebhookIntent } from './durable-entity-handler.js';
import { DURABLE_ENTITY_MAX_TRANSITION_BYTES, DURABLE_ENTITY_LOCAL_LIMITS, DURABLE_ENTITY_MAX_PENDING_OUTBOX } from './durable-entity-contract.js';

export interface LocalEntityInput { namespace: string; key: string; requestId: string; payload: unknown }
export interface LocalEntityResult { value: unknown; version: number; replayed: boolean }
export type LocalEntityFault = 'before-commit' | 'after-commit';
export class LocalEntityFaultError extends Error {
  constructor(readonly phase: LocalEntityFault) { super(`simulated ${phase} failure; retry the same request ID and payload`); }
}
interface Receipt { id: string; fingerprint: string; value: unknown; version: number }
export interface LocalEntityIntent extends DurableEntityWebhookIntent {
  message_id: string; state_version: number; ordinal: number;
}
interface Stored {
  namespace: string; key: string; data: unknown; version: number;
  alarm_at?: string; receipts: Receipt[]; outbox: LocalEntityIntent[];
}
function copy<T>(value: T): T { return JSON.parse(durableEntityJSON(value)) as T; }
function hash(value: string): string { return createHash('sha256').update(value).digest('hex'); }
function entityKey(namespace: string, key: string): string { return durableEntityJSON([namespace, key]); }

export class DurableEntitySimulator {
  private readonly entities = new Map<string, Stored>();
  private readonly queues = new Map<string, Promise<void>>();
  private readonly scope: Omit<DurableEntityIdentity, 'namespace' | 'key'>;
  private readonly limits: DurableEntityHandlerLimits = {...DURABLE_ENTITY_LOCAL_LIMITS};
  private time: number;
  constructor(private readonly registry: DurableEntityRegistry, options: {
    now?: Date;
    scope?: Omit<DurableEntityIdentity, 'namespace' | 'key'>;
    protocolVersion?: 1 | 2;
  } = {}) {
    this.scope = copy(options.scope ?? {account_id: '11111111-1111-4111-8111-111111111111', app_id: '22222222-2222-4222-8222-222222222222'});
    this.protocolVersion = options.protocolVersion ?? 2;
    if (this.protocolVersion !== 1 && this.protocolVersion !== 2) throw new TypeError('invalid local protocol version');
    this.time = (options.now ?? new Date('2000-01-01T00:00:00Z')).getTime();
    if (!Number.isFinite(this.time)) throw new TypeError('invalid simulator clock');
  }
  readonly protocolVersion: 1 | 2;
  now(): Date { return new Date(this.time); }
  advance(milliseconds: number): void {
    if (!Number.isSafeInteger(milliseconds) || milliseconds < 0 || !Number.isFinite(new Date(this.time + milliseconds).getTime())) throw new RangeError('invalid clock advance');
    this.time += milliseconds;
  }
  async invoke(input: LocalEntityInput, options: {fault?: LocalEntityFault} = {}): Promise<LocalEntityResult> {
    if (typeof input.requestId !== 'string') throw new TypeError('explicit request ID required');
    if (Buffer.byteLength(durableEntityJSON(input.payload)) > DURABLE_ENTITY_MAX_TRANSITION_BYTES) throw new RangeError('local invocation payload budget exceeded');
    if (input.requestId.startsWith('__gregale_alarm/')) return Promise.reject(new TypeError('reserved alarm request ID'));
    return this.execute(copy(input), 'invoke', options.fault);
  }
  private async serialized<T>(key: string, run: () => T): Promise<T> {
    const previous = this.queues.get(key) ?? Promise.resolve();
    let release!: () => void;
    const current = new Promise<void>(resolve => { release = resolve; });
    this.queues.set(key, current);
    await previous;
    try { return run(); }
    finally { release(); if (this.queues.get(key) === current) this.queues.delete(key); }
  }
  private execute(input: LocalEntityInput, event: 'invoke' | 'alarm', fault?: LocalEntityFault, alarmVersion?: number): Promise<LocalEntityResult> {
    if (fault !== undefined && fault !== 'before-commit' && fault !== 'after-commit') return Promise.reject(new TypeError('invalid local fault'));
    const fingerprint = hash(durableEntityJSON(input.payload));
    const key = entityKey(input.namespace, input.key);
    return this.serialized(key, () => {
      const prior = this.entities.get(key);
      const state = prior ?? {namespace: input.namespace, key: input.key, data: null, version: 0, receipts: [], outbox: []};
      const request = this.envelope(input, event, state);
      const receipt = state.receipts.find(saved => saved.id === input.requestId);
      if (receipt) {
        if (receipt.fingerprint !== fingerprint) throw new TypeError('request ID was already committed with a different payload');
        return copy({value: receipt.value, version: receipt.version, replayed: true});
      }
      if (event === 'alarm' && (state.version !== alarmVersion || !state.alarm_at || Date.parse(state.alarm_at) > this.time)) throw new TypeError('obsolete local alarm');
      if (!Number.isSafeInteger(state.version + 1)) throw new RangeError('local version exceeds safe integer range');
      const transition = JSON.parse(this.registry.handle(durableEntityJSON(request))) as {
        data: unknown; result: unknown; alarm_at?: string; outbox?: DurableEntityWebhookIntent[];
      };
      encodeDurableEntityTransition(request, transition);
      const outbox = [...state.outbox];
      for (const [ordinal, intent] of (transition.outbox ?? []).entries()) {
        outbox.push({...intent, message_id: hash(durableEntityJSON([this.scope, input.namespace, input.key, input.requestId, ordinal])), state_version: state.version + 1, ordinal});
      }
      if (outbox.length > DURABLE_ENTITY_MAX_PENDING_OUTBOX || Buffer.byteLength(durableEntityJSON(outbox)) > this.limits.outbox_bytes) throw new RangeError('local pending outbox budget exceeded');
      const next: Stored = {namespace: input.namespace, key: input.key, data: transition.data, version: state.version + 1,
        receipts: [...state.receipts, {id: input.requestId, fingerprint, value: transition.result, version: state.version + 1}], outbox};
      if (transition.alarm_at !== undefined) next.alarm_at = transition.alarm_at;
      // Approximate snapshot accounting; provider encoding, journals and storage quotas need live qualification.
      if (Buffer.byteLength(durableEntityJSON({data: next.data, result: transition.result, outbox, alarm_at: next.alarm_at ?? null})) > DURABLE_ENTITY_MAX_TRANSITION_BYTES) throw new RangeError('local snapshot budget exceeded');
      if (fault === 'before-commit') throw new LocalEntityFaultError(fault);
      this.entities.set(key, copy(next));
      if (fault === 'after-commit') throw new LocalEntityFaultError(fault);
      return copy({value: transition.result, version: next.version, replayed: false});
    });
  }
  private envelope(input: LocalEntityInput, event: 'invoke' | 'alarm', state: Stored) {
    const request = {protocol_version: this.protocolVersion, event, entity: {...this.scope, namespace: input.namespace, key: input.key},
      request_id: input.requestId, payload: input.payload, state: {data: state.data, version: state.version, ...(state.alarm_at ? {alarm_at: state.alarm_at} : {})},
      deployment_id: '33333333-3333-4333-8333-333333333333', ...(this.protocolVersion === 2 ? {limits: this.limits} : {})};
    return decodeDurableEntityHandlerRequest(durableEntityJSON(request));
  }
  inspect(namespace: string, key: string): Readonly<Stored> | undefined {
    const stored = this.entities.get(entityKey(namespace, key));
    return stored ? copy(stored) : undefined;
  }
  /** Records intents only. This simulator never sends a webhook or accepts delivery. */
  pendingIntents(namespace: string, key: string): LocalEntityIntent[] { return copy(this.entities.get(entityKey(namespace, key))?.outbox ?? []); }
  async runDueAlarms(): Promise<LocalEntityResult[]> {
    const due = [...this.entities.values()].filter(state => state.alarm_at && Date.parse(state.alarm_at) <= this.time).map(state => copy(state));
    const results: LocalEntityResult[] = [];
    for (const state of due) {
      const payload = {type: 'alarm', scheduled_at: state.alarm_at!, state_version: state.version};
      results.push(await this.execute({namespace: state.namespace, key: state.key, requestId: `__gregale_alarm/${state.version}`, payload}, 'alarm', undefined, state.version));
    }
    return results;
  }
  /** Local checkpoint includes private application data and replay history. Never upload it as a platform restore. */
  checkpoint(): string {
    if (this.queues.size) throw new TypeError('wait for local invocations before checkpointing');
    return durableEntityJSON({format: 1, scope: this.scope, protocol_version: this.protocolVersion, now: this.time, entities: [...this.entities.values()]});
  }
  restore(checkpoint: string): void {
    if (this.queues.size || this.entities.size || Buffer.byteLength(checkpoint) > (16 << 20)) throw new TypeError('restore requires an empty idle simulator and a bounded checkpoint');
    const saved = JSON.parse(checkpoint) as {format: number; scope: unknown; protocol_version: number; now: number; entities: Stored[]};
    if (saved.format !== 1 || durableEntityJSON(saved.scope) !== durableEntityJSON(this.scope) || saved.protocol_version !== this.protocolVersion
      || typeof saved.now !== 'number' || !Number.isFinite(new Date(saved.now).getTime()) || !Array.isArray(saved.entities)) throw new TypeError('invalid local checkpoint scope or format');
    const restored = new Map<string, Stored>();
    for (const state of saved.entities) {
      if (!Number.isSafeInteger(state.version) || state.version <= 0 || !Array.isArray(state.receipts) || !Array.isArray(state.outbox)) throw new TypeError('invalid local checkpoint entity');
      this.envelope({namespace: state.namespace, key: state.key, requestId: 'checkpoint-validation', payload: null}, 'invoke', state);
      const ids = new Set<string>();
      for (const receipt of state.receipts) {
        if (typeof receipt.id !== 'string' || !receipt.id || ids.has(receipt.id) || !/^[a-f0-9]{64}$/u.test(receipt.fingerprint)
          || !Number.isSafeInteger(receipt.version) || receipt.version <= 0 || receipt.version > state.version) throw new TypeError('invalid local checkpoint receipt');
        durableEntityJSON(receipt.value); ids.add(receipt.id);
      }
      if (Buffer.byteLength(durableEntityJSON(state.data)) > DURABLE_ENTITY_MAX_TRANSITION_BYTES) throw new TypeError('checkpoint state is too large');
      const messages = new Set<string>();
      for (const intent of state.outbox) {
        if (typeof intent.message_id !== 'string' || !/^[a-f0-9]{64}$/u.test(intent.message_id) || messages.has(intent.message_id)
          || !Number.isSafeInteger(intent.state_version) || intent.state_version <= 0 || intent.state_version > state.version
          || !Number.isSafeInteger(intent.ordinal) || intent.ordinal < 0 || intent.ordinal >= this.limits.outbox_messages) throw new TypeError('invalid checkpoint intent');
        messages.add(intent.message_id);
      }
      const intents = state.outbox.map(({webhook_id, event_type, payload}) => ({webhook_id, event_type, payload}));
      // Validate each intent independently of the per-transition batch limit.
      for (const intent of intents) encodeDurableEntityTransition(this.envelope({namespace: state.namespace, key: state.key, requestId: 'checkpoint-validation', payload: null}, 'invoke', state), {data: state.data, result: null, outbox: [intent]});
      if (state.outbox.length > DURABLE_ENTITY_MAX_PENDING_OUTBOX || Buffer.byteLength(durableEntityJSON(state.outbox)) > this.limits.outbox_bytes) throw new TypeError('invalid checkpoint outbox');
      const key = entityKey(state.namespace, state.key);
      if (restored.has(key)) throw new TypeError('duplicate checkpoint entity');
      restored.set(key, copy(state));
    }
    for (const [key, state] of restored) this.entities.set(key, state);
    this.time = saved.now;
  }
}
