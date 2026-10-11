// A pure guest computation. Mount it behind the existing trusted handler route;
// parsing an envelope is not authentication for a public endpoint.
import { decodeDurableEntityCall } from '@gregale/sdk-node';

type Counter = { count: number };
type Input = { delta?: number };

function counter(value: unknown): Counter {
  if (value === null || typeof value !== 'object' || !('count' in value)
    || !Number.isSafeInteger(value.count)) throw new TypeError('invalid counter state');
  return { count: value.count as number };
}

function input(value: unknown, event: 'invoke' | 'alarm'): Input {
  if (event === 'alarm') return {};
  if (value === null || typeof value !== 'object' || !('delta' in value)
    || !Number.isSafeInteger(value.delta)) throw new TypeError('invalid counter input');
  return { delta: value.delta as number };
}

export function counterTransition(body: string | Uint8Array, webhookId: string): string {
  const call = decodeDurableEntityCall(body, { initialState: () => ({ count: 0 }), decodeState: counter, decodePayload: input });
  if (call.entity.namespace !== 'counters') throw new TypeError('unexpected entity namespace');
  if (call.event === 'alarm') {
    // Explicitly consume the due alarm; producing this response performs no send.
    return call.transition(call.state, null).clearAlarm()
      .webhook(webhookId, 'counter.reminder', { key: call.entity.key, count: call.state.count }).encode();
  }
  const count = call.state.count + call.payload.delta!;
  if (!Number.isSafeInteger(count)) throw new RangeError('counter overflow');
  // Existing alarms are preserved. An application can choose scheduleAlarm()
  // with an explicit deterministic business deadline instead.
  return call.transition({ count }, { count }).encode();
}
