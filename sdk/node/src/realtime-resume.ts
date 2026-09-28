/** Client-held cursor consumer for the opt-in managed realtime v2 protocol. */

export const REALTIME_RESUME_SUBPROTOCOL = 'gregale.realtime.v2';

export interface RealtimeCursorStore {
  /** Return the last fully processed sequence, or 0 for a new channel. */
  load(): number | Promise<number>;
  /** Durably commit a processed sequence before it can be skipped on reconnect. */
  save(sequence: number): void | Promise<void>;
}

export interface RealtimeMessage {
  channel: string;
  sequence: number;
  messageId: string;
  data: Uint8Array;
  binary: boolean;
}

/** The small WebSocket surface used by this helper, compatible with `ws`. */
export interface RealtimeSocket {
  readonly protocol: string;
  readonly readyState: number;
  addEventListener(type: 'open' | 'message' | 'close' | 'error', listener: (event: unknown) => void): void;
  removeEventListener(type: 'open' | 'message' | 'close' | 'error', listener: (event: unknown) => void): void;
  send(data: string): void;
  close(): void;
}

export interface ConsumeRealtimeChannelOptions {
  /** Full wss:// endpoint URL, including the endpoint ID. */
  url: string;
  channel: string;
  cursorStore: RealtimeCursorStore;
  /** Commit the application side effect before this callback resolves. */
  onMessage(message: RealtimeMessage): void | Promise<void>;
  /** Supply a WebSocket constructor that can add the OIDC bearer header. */
  webSocketFactory(url: string, protocols: string[]): RealtimeSocket | Promise<RealtimeSocket>;
  signal?: AbortSignal;
  retryInitialMs?: number;
  retryMaxMs?: number;
}

export class RealtimeResyncRequiredError extends Error {
  constructor(
    readonly channel: string,
    readonly oldestSequence: number,
    readonly latestSequence: number,
  ) {
    super(`realtime history unavailable for channel ${channel}; rebuild state before resubscribing`);
    this.name = 'RealtimeResyncRequiredError';
  }
}

export class RealtimeProtocolError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RealtimeProtocolError';
  }
}

class SocketSendError extends Error {}

type SocketEvent = { type: 'open' } | { type: 'close' } | { type: 'abort' } | { type: 'message'; data: unknown };

function eventsFor(socket: RealtimeSocket, signal?: AbortSignal): {
  next(): Promise<SocketEvent>;
  dispose(): void;
} {
  const queued: SocketEvent[] = [];
  let waiter: ((event: SocketEvent) => void) | undefined;
  const push = (event: SocketEvent) => {
    if (waiter) {
      const resolve = waiter;
      waiter = undefined;
      resolve(event);
    } else if (queued.length < 128) {
      queued.push(event);
    } else {
      // A slow consumer must reconnect from its persisted cursor, not hold
      // an unbounded backlog in this process.
      queued.length = 0;
      queued.push({ type: 'close' });
      try { socket.close(); } catch { /* connection is already gone */ }
    }
  };
  const open = () => push({ type: 'open' });
  const message = (event: unknown) => push({ type: 'message', data: (event as { data?: unknown }).data });
  const close = () => push({ type: 'close' });
  const error = () => push({ type: 'close' });
  const abort = () => {
    push({ type: 'abort' });
    try { socket.close(); } catch { /* connection is already gone */ }
  };
  socket.addEventListener('open', open);
  socket.addEventListener('message', message);
  socket.addEventListener('close', close);
  socket.addEventListener('error', error);
  signal?.addEventListener('abort', abort, { once: true });
  if (socket.readyState === 1) push({ type: 'open' });
  if (signal?.aborted) abort();
  return {
    next: () => queued.length ? Promise.resolve(queued.shift()!) : new Promise((resolve) => { waiter = resolve; }),
    dispose: () => {
      socket.removeEventListener('open', open);
      socket.removeEventListener('message', message);
      socket.removeEventListener('close', close);
      socket.removeEventListener('error', error);
      signal?.removeEventListener('abort', abort);
      try { socket.close(); } catch { /* connection is already gone */ }
    },
  };
}

function sequence(value: unknown, name: string): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) {
    throw new RealtimeProtocolError(`invalid realtime ${name}`);
  }
  return value;
}

function frameOf(data: unknown): Record<string, unknown> {
  if (typeof data !== 'string') throw new RealtimeProtocolError('realtime v2 requires JSON text frames');
  let frame: unknown;
  try { frame = JSON.parse(data); } catch { throw new RealtimeProtocolError('invalid realtime JSON frame'); }
  if (frame === null || typeof frame !== 'object' || Array.isArray(frame)) {
    throw new RealtimeProtocolError('invalid realtime frame');
  }
  return frame as Record<string, unknown>;
}

function decodeMessage(frame: Record<string, unknown>, channel: string, after: number): RealtimeMessage {
  const next = sequence(frame.sequence, 'message sequence');
  if (frame.channel !== channel || next !== after + 1 ||
      typeof frame.message_id !== 'string' || frame.message_id === '' ||
      (frame.data_base64 !== undefined && typeof frame.data_base64 !== 'string') ||
      !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(frame.data_base64 ?? '') ||
      (frame.binary !== undefined && typeof frame.binary !== 'boolean')) {
    throw new RealtimeProtocolError('invalid or out-of-order realtime message');
  }
  return {
    channel, sequence: next, messageId: frame.message_id,
    data: Buffer.from(frame.data_base64 ?? '', 'base64'), binary: frame.binary ?? false,
  };
}

function pause(ms: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted || ms <= 0) return Promise.resolve();
  return new Promise((resolve) => {
    const timer = setTimeout(done, ms);
    function done() {
      clearTimeout(timer);
      signal?.removeEventListener('abort', done);
      resolve();
    }
    signal?.addEventListener('abort', done, { once: true });
    if (signal?.aborted) done();
  });
}

/**
 * Consume one v2 channel until aborted. The cursor advances only after both
 * `onMessage` and `cursorStore.save` succeed. A disconnect reconnects with
 * the saved cursor; application processing remains at least once.
 *
 * A stale cursor throws `RealtimeResyncRequiredError`. Rebuild application
 * state and set a new cursor explicitly before starting another consumer.
 */
export async function consumeRealtimeChannel(options: ConsumeRealtimeChannelOptions): Promise<void> {
  const url = new URL(options.url);
  if (url.protocol !== 'wss:' && url.protocol !== 'ws:') throw new TypeError('realtime URL must use wss or ws');
  if (!options.channel || Buffer.byteLength(options.channel, 'utf8') > 256 || options.channel.trim() !== options.channel ||
      /[/?#\r\n]/.test(options.channel)) throw new TypeError('invalid realtime channel');
  const initialDelay = options.retryInitialMs ?? 100;
  const maxDelay = options.retryMaxMs ?? 5_000;
  if (!Number.isFinite(initialDelay) || initialDelay < 0 || !Number.isFinite(maxDelay) || maxDelay < initialDelay) {
    throw new TypeError('invalid realtime retry delay');
  }
  let after = sequence(await options.cursorStore.load(), 'stored cursor');
  let delay = initialDelay;
  while (!options.signal?.aborted) {
    const socket = await options.webSocketFactory(url.href, [REALTIME_RESUME_SUBPROTOCOL]);
    const events = eventsFor(socket, options.signal);
    let connected = false;
    let subscribed = false;
    try {
      while (!options.signal?.aborted) {
        const event = await events.next();
        if (event.type === 'abort' || event.type === 'close') break;
        if (event.type === 'open') {
          if (connected) continue;
          if (socket.protocol !== REALTIME_RESUME_SUBPROTOCOL) {
            throw new RealtimeProtocolError('realtime v2 subprotocol was not negotiated');
          }
          connected = true;
          try { socket.send(JSON.stringify({ type: 'subscribe', channel: options.channel, after })); }
          catch { throw new SocketSendError(); }
          continue;
        }
        if (!connected) throw new RealtimeProtocolError('realtime message before WebSocket open');
        const frame = frameOf(event.data);
        if (frame.channel !== options.channel) throw new RealtimeProtocolError('unexpected realtime channel');
        switch (frame.type) {
          case 'subscribed':
            if (subscribed || sequence(frame.sequence, 'subscription sequence') !== after) {
              throw new RealtimeProtocolError('invalid realtime subscription response');
            }
            subscribed = true;
            delay = initialDelay;
            break;
          case 'message': {
            if (!subscribed) throw new RealtimeProtocolError('realtime message before subscription');
            const message = decodeMessage(frame, options.channel, after);
            await options.onMessage(message);
            await options.cursorStore.save(message.sequence);
            after = message.sequence;
            try { socket.send(JSON.stringify({ type: 'ack', channel: options.channel, sequence: after })); }
            catch { throw new SocketSendError(); }
            break;
          }
          case 'acknowledged':
            if (!subscribed) throw new RealtimeProtocolError('realtime ack before subscription');
            sequence(frame.sequence, 'acknowledged sequence');
            break;
          case 'resync_required':
            throw new RealtimeResyncRequiredError(options.channel,
              sequence(frame.oldest_sequence, 'oldest sequence'),
              sequence(frame.latest_sequence, 'latest sequence'));
          case 'error':
            if (frame.code === 'history_read_failed') break;
            throw new RealtimeProtocolError(`realtime subscription failed: ${String(frame.code)}`);
          default:
            throw new RealtimeProtocolError('unknown realtime frame');
        }
        if (frame.type === 'error' && frame.code === 'history_read_failed') break;
      }
    } catch (error) {
      if (!(error instanceof SocketSendError)) throw error;
    } finally {
      events.dispose();
    }
    if (options.signal?.aborted) return;
    await pause(delay, options.signal);
    delay = Math.min(Math.max(delay * 2, initialDelay), maxDelay);
  }
}
