// ADR-938: typed guest computation, without client/transport dependencies.
import {
  decodeDurableEntityHandlerRequest, encodeDurableEntityTransition, durableEntityWebhookIntent,
  type DurableEntityHandlerRequest, type DurableEntityIdentity, type DurableEntityTransition,
} from './durable-entity-handler.js';

export interface DurableEntityCallOptions<State, Payload> {
  initialState: () => State;
  decodeState: (value: unknown) => State;
  decodePayload: (value: unknown, event: 'invoke' | 'alarm') => Payload;
}

export interface DurableEntityCall<State, Payload> {
  readonly entity: Readonly<DurableEntityIdentity>;
  readonly requestId: string;
  readonly event: 'invoke' | 'alarm';
  readonly version: number;
  readonly state: State;
  readonly payload: Payload;
  transition<Result>(data: State, result: Result): DurableEntityTransitionBuilder<State, Result>;
}

export function decodeDurableEntityCall<State, Payload>(body: string | Uint8Array, options: DurableEntityCallOptions<State, Payload>): DurableEntityCall<State, Payload> {
  const request = decodeDurableEntityHandlerRequest(body);
  // Initial values are isolated through the same strict JSON/budget checks as transitions.
  const initial: unknown = request.state.version === 0
    ? (JSON.parse(encodeDurableEntityTransition(request, { data: options.initialState(), result: null })) as { data: unknown }).data
    : request.state.data;
  const state = options.decodeState(initial);
  const payload = options.decodePayload(request.payload, request.event === 'alarm' ? 'alarm' : 'invoke');
  return Object.freeze({
    entity: Object.freeze({ ...request.entity }), requestId: request.request_id,
    event: request.event === 'alarm' ? 'alarm' as const : 'invoke' as const,
    version: request.state.version, state, payload,
    transition<Result>(data: State, result: Result) { return new DurableEntityTransitionBuilder(request, data, result); },
  });
}

export class DurableEntityTransitionBuilder<State, Result> {
  private readonly next: DurableEntityTransition<State, Result>;
  private rejected: unknown;
  private failed = false;
  constructor(private readonly request: DurableEntityHandlerRequest, data: State, result: Result, private readonly validateData?: (value: State) => State) {
    this.next = { data, result };
    if (request.state.alarm_at !== undefined) this.next.alarm_at = request.state.alarm_at;
  }
  scheduleAlarm(at: string | Date): this {
    if (this.failed) throw this.rejected;
    try { this.next.alarm_at = typeof at === 'string' ? at : at.toISOString(); }
    catch (cause) { this.failed = true; this.rejected = cause; throw cause; }
    return this;
  }
  clearAlarm(): this { delete this.next.alarm_at; return this; }
  webhook(webhookId: string, event: string, payload: unknown): this {
    if (this.failed) throw this.rejected;
    try {
      const intent = durableEntityWebhookIntent(this.request, webhookId, event, payload);
      (this.next.outbox ??= []).push(intent);
    } catch (cause) { this.failed = true; this.rejected = cause; throw cause; }
    return this;
  }
  encode(): string {
    if (this.failed) throw this.rejected;
    if (this.validateData) this.next.data = this.validateData(this.next.data);
    return encodeDurableEntityTransition(this.request, this.next);
  }
}
