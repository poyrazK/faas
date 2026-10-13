/** Durable application-session ordering, independent of the socket carrying frames. */
import {defineDurableEntity} from './durable-entity-runtime.js';
import {durableEntityJSON} from './durable-entity-handler.js';
export {defineRecoverableDurableSessionEntity, decodeDurableSessionLifecycleCommand, decodeDurableSessionLifecycleState, durableSessionLifecycleRequestID, durableSessionLifecycleFrame, decodeDurableSessionStatus, decodeDurableSessionLifecycleAcknowledgement, type DurableSessionLifecycleCommand, type DurableSessionLifecycleState, type DurableSessionLifecycleFrame} from './durable-entity-session-lifecycle.js';
export const DURABLE_ENTITY_MAX_SESSIONS = 32;
interface Session {id: string; sequence: number; closed: boolean}
export type DurableSessionCommand<Payload = unknown> = {kind: 'open'; session_id: string} | {kind: 'message'; session_id: string; sequence: number; payload: Payload} | {kind: 'close'; session_id: string; sequence: number};
export interface DurableSessionState<State> {application: State; sessions: Session[]}
function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw new TypeError('invalid session object');
  return value as Record<string, unknown>;
}
function sessionID(value: unknown): value is string {return typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(value);}
export function durableSessionRequestID(command: DurableSessionCommand): string {
  return `session/${command.session_id}/${command.kind === 'open' ? 'open' : command.kind+'/'+command.sequence}`;
}
export function decodeDurableSessionCommand(value: unknown): DurableSessionCommand {
  const input = object(value);
  if (!sessionID(input.session_id)) throw new TypeError('canonical UUIDv4 session identity required');
  if (input.kind === 'open' && Object.keys(input).length === 2) return {kind: 'open', session_id: input.session_id};
  if ((input.kind === 'message' || input.kind === 'close') && Number.isSafeInteger(input.sequence) && (input.sequence as number)>0
    && Object.keys(input).length === (input.kind === 'message' ? 4 : 3) && (input.kind !== 'message' || Object.hasOwn(input, 'payload'))) {
    if (input.kind === 'message') return {kind:'message', session_id:input.session_id, sequence:input.sequence as number, payload:input.payload};
    return {kind:'close', session_id:input.session_id, sequence:input.sequence as number};
  }
  throw new TypeError('invalid ordered session command');
}
export function defineDurableSessionEntity<State, Payload, Result>(options: {
  namespace: string; initialState: () => State; decodeState: (value: unknown) => State;
  decodePayload: (value: unknown) => Payload;
  handle: (input: {state: State; payload: Payload; sessionId: string; sequence: number}) => {state: State; result: Result};
}) {
  const {namespace, initialState, decodeState, decodePayload, handle} = options;
  return defineDurableEntity({namespace, schema: {
    version: 1, migrations: new Map(),
    initialState: () => ({application: initialState(), sessions: [] as Session[]}),
    decodeState(value: unknown): DurableSessionState<State> {
      const state = object(value);
      if (Object.keys(state).length !== 2 || !Array.isArray(state.sessions) || state.sessions.length > DURABLE_ENTITY_MAX_SESSIONS) throw new TypeError('invalid session state');
      const ids = new Set<string>();
      const sessions = state.sessions.map(value => {
        const item = object(value);
        if (Object.keys(item).length !== 3 || !sessionID(item.id) || ids.has(item.id) || !Number.isSafeInteger(item.sequence) || (item.sequence as number)<0 || typeof item.closed !== 'boolean') throw new TypeError('invalid retained session');
        ids.add(item.id); return {id:item.id, sequence:item.sequence as number, closed:item.closed};
      });
      return {application:decodeState(state.application),sessions};
    },
  }, decodePayload(value, event) {if (event !== 'invoke') throw new TypeError('session alarms unsupported'); return decodeDurableSessionCommand(value);},
  handle(call) {
    const command = call.payload;
    if (call.requestId !== durableSessionRequestID(command)) throw new TypeError('stable session request identity required');
    const state = call.state;
    let session = state.sessions.find(item => item.id === command.session_id);
    if (command.kind === 'open') {
      if (session || state.sessions.length >= DURABLE_ENTITY_MAX_SESSIONS) throw new TypeError('session already retained or session budget exhausted');
      session = {id:command.session_id, sequence:0, closed:false}; state.sessions.push(session);
      return call.transition(state, {session_id:session.id, sequence:0, closed:false, value:null as unknown});
    }
    if (!session || session.closed || command.sequence !== session.sequence+1) throw new TypeError('session closed, unknown or sequence gap');
    let value: unknown = null;
    if (command.kind === 'message') {
      const next = handle({state:state.application,payload:decodePayload(command.payload),sessionId:session.id,sequence:command.sequence});
      state.application = decodeState(next.state); value = JSON.parse(durableEntityJSON(next.result)) as unknown;
    } else session.closed = true;
    session.sequence = command.sequence;
    return call.transition(state, {session_id:session.id, sequence:session.sequence, closed:session.closed, value});
  }});
}
export interface DurableSessionFrame {protocol_version: 1; request_id: string; payload: DurableSessionCommand}
/** Prepare one frame. Save its exact bytes before sending; reuse it after disconnect. */
export function durableSessionFrame(command: DurableSessionCommand): DurableSessionFrame {
  const payload = decodeDurableSessionCommand(JSON.parse(durableEntityJSON(command)) as unknown);
  const frame: DurableSessionFrame = {protocol_version:1,request_id:durableSessionRequestID(payload),payload};
  if (Buffer.byteLength(durableEntityJSON(frame))>65536) throw new RangeError('session frame exceeds transport budget');
  return frame;
}

/** Failed or invalid acknowledgements never authorize advancing the session cursor. */
export class DurableSessionAcknowledgementError extends Error {
  constructor(readonly status?: number) {super('session acknowledgement unavailable or invalid; retain and replay the original frame');}
}
export function decodeDurableSessionAcknowledgement<Result>(value: unknown, pending: DurableSessionFrame, decodeValue: (value: unknown) => Result) {
  const reply=object(value);
  if (reply.protocol_version!==1 || reply.request_id!==pending.request_id || !Number.isInteger(reply.status)) throw new DurableSessionAcknowledgementError();
  if (reply.status!==200) throw new DurableSessionAcknowledgementError(reply.status as number);
  const committed=object(reply.result);
  if (!Number.isSafeInteger(committed.version)||(committed.version as number)<=0||typeof committed.replayed!=='boolean') throw new DurableSessionAcknowledgementError(200);
  const result=object(committed.value);
  const sequence=pending.payload.kind==='open'?0:pending.payload.sequence;
  if (result.session_id!==pending.payload.session_id || result.sequence!==sequence || result.closed!==(pending.payload.kind==='close')) throw new DurableSessionAcknowledgementError(200);
  return {version:committed.version as number,replayed:committed.replayed,sessionId:result.session_id as string,sequence,closed:result.closed as boolean,value:decodeValue(result.value)};
}
