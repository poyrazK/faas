/** Recoverable sessions use generations to fence requests after cohort reclamation. */
import {defineDurableEntity} from './durable-entity-runtime.js';
import {durableEntityJSON} from './durable-entity-handler.js';
import {decodeDurableSessionCommand, DURABLE_ENTITY_MAX_SESSIONS, type DurableSessionCommand} from './durable-entity-session.js';

export type DurableSessionLifecycleCommand<Payload = unknown> =
  | (DurableSessionCommand<Payload> & {generation: number})
  | {kind: 'status'; session_id: string; query_id: string}
  | {kind: 'advance'; generation: number; tick: number; operation_id: string}
  | {kind: 'reclaim'; generation: number; operation_id: string};
interface RetainedSession {id: string; sequence: number; closed: boolean; expired: boolean; active_at: number; closed_at: number | null}
export interface DurableSessionLifecycleState<State> {application: State; generation: number; tick: number; sessions: RetainedSession[]}
export interface DurableSessionLifecycleFrame {protocol_version: 1; request_id: string; payload: DurableSessionLifecycleCommand}
function record(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw new TypeError('invalid lifecycle object');
  return value as Record<string, unknown>;
}
function integer(value: unknown): value is number {return Number.isSafeInteger(value) && (value as number) >= 0;}
function uuid(value: unknown): value is string {return typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(value);}
export function decodeDurableSessionLifecycleCommand(value: unknown): DurableSessionLifecycleCommand {
  const input = record(value);
  if (input.kind === 'status' && Object.keys(input).length === 3 && uuid(input.session_id) && uuid(input.query_id)) {
    return {kind: 'status', session_id: input.session_id, query_id: input.query_id};
  }
  if (!integer(input.generation)) throw new TypeError('session generation required');
  if (input.kind === 'advance' && Object.keys(input).length === 4 && integer(input.tick) && uuid(input.operation_id)) {
    return {kind: 'advance', generation: input.generation, tick: input.tick, operation_id: input.operation_id};
  }
  if (input.kind === 'reclaim' && Object.keys(input).length === 3 && uuid(input.operation_id)) {
    return {kind: 'reclaim', generation: input.generation, operation_id: input.operation_id};
  }
  const {generation, ...command} = input;
  return {...decodeDurableSessionCommand(command), generation: generation as number};
}
export function durableSessionLifecycleRequestID(command: DurableSessionLifecycleCommand): string {
  if (command.kind === 'status') return `session/status/${command.session_id}/${command.query_id}`;
  if (command.kind === 'advance' || command.kind === 'reclaim') return `session/g${command.generation}/${command.kind}/${command.operation_id}`;
  return `session/g${command.generation}/${command.session_id}/${command.kind === 'open' ? 'open' : command.kind + '/' + command.sequence}`;
}
export function durableSessionLifecycleFrame(command: DurableSessionLifecycleCommand): DurableSessionLifecycleFrame {
  const payload = decodeDurableSessionLifecycleCommand(JSON.parse(durableEntityJSON(command)) as unknown);
  const frame: DurableSessionLifecycleFrame = {protocol_version: 1, request_id: durableSessionLifecycleRequestID(payload), payload};
  if (Buffer.byteLength(durableEntityJSON(frame)) > 65536) throw new RangeError('session frame exceeds transport budget');
  return frame;
}
export function decodeDurableSessionLifecycleState<State>(value: unknown, decodeApplication: (value: unknown) => State): DurableSessionLifecycleState<State> {
  const state = record(value);
  if (Object.keys(state).length !== 4 || !integer(state.generation) || !integer(state.tick) || !Array.isArray(state.sessions) || state.sessions.length > DURABLE_ENTITY_MAX_SESSIONS) throw new TypeError('invalid lifecycle state');
  const ids = new Set<string>();
  const sessions = state.sessions.map(value => {
    const item = record(value);
    if (Object.keys(item).length !== 6 || !uuid(item.id) || ids.has(item.id) || !integer(item.sequence) || typeof item.closed !== 'boolean' || typeof item.expired !== 'boolean'
      || !integer(item.active_at) || item.active_at > (state.tick as number)
      || (item.closed ? !integer(item.closed_at) || item.closed_at < item.active_at || item.closed_at > (state.tick as number) : item.closed_at !== null || item.expired)) throw new TypeError('invalid retained lifecycle session');
    ids.add(item.id);
    return {id: item.id, sequence: item.sequence, closed: item.closed, expired: item.expired, active_at: item.active_at, closed_at: item.closed_at as number | null};
  });
  return {application: decodeApplication(state.application), generation: state.generation, tick: state.tick, sessions};
}

export function defineRecoverableDurableSessionEntity<State, Payload, Result>(options: {
  namespace: string; initialState: () => State; decodeState: (value: unknown) => State;
  decodePayload: (value: unknown) => Payload;
  handle: (input: {state: State; payload: Payload; sessionId: string; sequence: number}) => {state: State; result: Result};
  /** Authenticated service advances this monotonic logical clock; no guest wall clock or automatic expiry. */
  idleTicks: number; retentionTicks: number;
  /** Version 2 introduces lifecycle metadata. Each later version needs an application-only n -> n+1 migration. */
  schemaVersion?: number; applicationMigrations?: ReadonlyMap<number, (application: unknown) => unknown>;
}) {
  const {namespace, initialState, decodeState, decodePayload, handle, idleTicks, retentionTicks} = options;
  const version = options.schemaVersion ?? 2;
  if (!integer(idleTicks) || idleTicks === 0 || !integer(retentionTicks) || retentionTicks === 0 || !Number.isInteger(version) || version < 2 || version > 0xffffffff) throw new TypeError('invalid lifecycle policy');
  const applicationMigrations = new Map(options.applicationMigrations);
  if (version - 2 > applicationMigrations.size) throw new TypeError('missing application migration');
  const migrations = new Map<number, (value: unknown) => unknown>();
  migrations.set(1, value => {
    const old = record(value);
    if (Object.keys(old).length !== 2 || !Array.isArray(old.sessions) || old.sessions.length > DURABLE_ENTITY_MAX_SESSIONS) throw new TypeError('invalid legacy session state');
    return {application: old.application, generation: 0, tick: 0, sessions: old.sessions.map(value => {
      const item = record(value);
      if (Object.keys(item).length !== 3 || !uuid(item.id) || !integer(item.sequence) || typeof item.closed !== 'boolean') throw new TypeError('invalid legacy retained session');
      return {...item, active_at: 0, closed_at: item.closed ? 0 : null, expired: false};
    })};
  });
  for (let n = 2; n < version; n++) {
    const migrate = applicationMigrations.get(n);
    if (typeof migrate !== 'function') throw new TypeError('missing application migration');
    migrations.set(n, value => {
      const state = record(value);
      const application = migrate(JSON.parse(durableEntityJSON(state.application)) as unknown);
      if (application !== null && typeof application === 'object' && typeof (application as {then?: unknown}).then === 'function') throw new TypeError('async migration unsupported');
      return {...state, application};
    });
  }
  return defineDurableEntity<DurableSessionLifecycleState<State>, DurableSessionLifecycleCommand, unknown>({namespace, schema: {version, migrations,
    initialState: (): DurableSessionLifecycleState<State> => ({application: initialState(), generation: 0, tick: 0, sessions: []}),
    decodeState: value => decodeDurableSessionLifecycleState(value, decodeState),
  }, decodePayload(value, event) {
    if (event !== 'invoke') throw new TypeError('session alarms unsupported');
    return decodeDurableSessionLifecycleCommand(value);
  }, handle(call) {
    const command = call.payload;
    if (call.requestId !== durableSessionLifecycleRequestID(command)) throw new TypeError('stable lifecycle request identity required');
    const state = call.state;
    if (command.kind === 'status') {
      const session = state.sessions.find(item => item.id === command.session_id);
      return call.transition(state, {kind: 'status', session_id: command.session_id, query_id: command.query_id, generation: state.generation, tick: state.tick,
        exists: !!session, sequence: session?.sequence ?? null, closed: session?.closed ?? null, expired: session?.expired ?? null});
    }
    if (command.generation !== state.generation) throw new TypeError('retired or future session generation');
    if (command.kind === 'advance') {
      if (command.tick < state.tick) throw new TypeError('logical clock cannot regress');
      state.tick = command.tick;
      for (const session of state.sessions) {
        if (!session.closed && state.tick - session.active_at >= idleTicks) {session.closed = true; session.expired = true; session.closed_at = state.tick;}
      }
      return call.transition(state, {kind: 'advance', generation: state.generation, tick: state.tick});
    }
    if (command.kind === 'reclaim') {
      if (!state.sessions.length || state.sessions.some(session => !session.closed || session.closed_at === null || state.tick - session.closed_at < retentionTicks)) throw new TypeError('cohort remains active or retained');
      if (state.generation === Number.MAX_SAFE_INTEGER) throw new RangeError('session generation exhausted');
      state.generation++; state.sessions = [];
      return call.transition(state, {kind: 'reclaim', generation: state.generation, tick: state.tick});
    }
    let session = state.sessions.find(item => item.id === command.session_id);
    let value: unknown = null;
    if (command.kind === 'open') {
      if (session || state.sessions.length >= DURABLE_ENTITY_MAX_SESSIONS) throw new TypeError('session already retained or session budget exhausted');
      session = {id: command.session_id, sequence: 0, closed: false, expired: false, active_at: state.tick, closed_at: null};
      state.sessions.push(session);
    } else {
      if (!session || session.closed || command.sequence !== session.sequence + 1) throw new TypeError('session closed, unknown or sequence gap');
      if (command.kind === 'message') {
        const next = handle({state: state.application, payload: decodePayload(command.payload), sessionId: session.id, sequence: command.sequence});
        state.application = decodeState(next.state); value = JSON.parse(durableEntityJSON(next.result)) as unknown;
      } else {session.closed = true; session.closed_at = state.tick;}
      session.sequence = command.sequence; session.active_at = state.tick;
    }
    return call.transition(state, {kind: command.kind, session_id: session.id, generation: state.generation, sequence: session.sequence, closed: session.closed, expired: session.expired, value});
  }});
}

/** A status is a committed snapshot, not a lease. Replaying a query returns its original snapshot. */
export function decodeDurableSessionStatus(value: unknown, pending: DurableSessionLifecycleFrame) {
  if (pending.payload.kind !== 'status') throw new TypeError('status frame required');
  const reply = record(value), committed = record(reply.result), result = record(committed.value);
  if (reply.protocol_version !== 1 || reply.request_id !== pending.request_id || reply.status !== 200 || !integer(committed.version) || committed.version === 0 || typeof committed.replayed !== 'boolean'
    || result.kind !== 'status' || result.session_id !== pending.payload.session_id || result.query_id !== pending.payload.query_id || !integer(result.generation) || !integer(result.tick) || typeof result.exists !== 'boolean'
    || (result.exists ? !integer(result.sequence) || typeof result.closed !== 'boolean' || typeof result.expired !== 'boolean' || result.expired && !result.closed : result.sequence !== null || result.closed !== null || result.expired !== null)) throw new TypeError('invalid session status acknowledgement');
  return {version: committed.version, replayed: committed.replayed, sessionId: result.session_id as string, generation: result.generation, tick: result.tick,
    exists: result.exists, sequence: result.sequence as number | null, closed: result.closed as boolean | null, expired: result.expired as boolean | null};
}

/** Match a committed command result before advancing local progress. Keep pending bytes on any failure. */
export function decodeDurableSessionLifecycleAcknowledgement<Result>(value: unknown, pending: DurableSessionLifecycleFrame, decodeValue: (value: unknown) => Result) {
  if (pending.payload.kind === 'status') throw new TypeError('use the session status decoder');
  const reply = record(value), committed = record(reply.result), result = record(committed.value), command = pending.payload;
  if (reply.protocol_version !== 1 || reply.request_id !== pending.request_id || reply.status !== 200 || !integer(committed.version) || committed.version === 0 || typeof committed.replayed !== 'boolean'
    || result.kind !== command.kind || !integer(result.generation) || result.generation !== command.generation + (command.kind === 'reclaim' ? 1 : 0)) throw new TypeError('invalid lifecycle acknowledgement; retain pending frame');
  const metadata = {version: committed.version, replayed: committed.replayed, generation: result.generation as number};
  if (command.kind === 'advance' || command.kind === 'reclaim') {
    if (!integer(result.tick) || command.kind === 'advance' && result.tick !== command.tick) throw new TypeError('invalid lifecycle tick');
    return {...metadata, kind: command.kind, tick: result.tick};
  }
  const sequence = command.kind === 'open' ? 0 : command.sequence;
  if (result.session_id !== command.session_id || result.sequence !== sequence || result.closed !== (command.kind === 'close') || result.expired !== false
    || command.kind !== 'message' && result.value !== null) throw new TypeError('invalid session progress');
  return {...metadata, kind: command.kind, sessionId: command.session_id, sequence, closed: result.closed as boolean, value: decodeValue(result.value)};
}
