/** Client-held cursor consumer for the opt-in managed realtime v2 protocol. */

export const REALTIME_RESUME_SUBPROTOCOL = 'gregale.realtime.v2';
export const REALTIME_MAX_CHANNELS_PER_CONNECTION = 8;

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

export interface RealtimeChannelConsumerOptions {
  channel: string;
  cursorStore: RealtimeCursorStore;
  /** Commit the application side effect before this callback resolves. */
  onMessage(message: RealtimeMessage): void | Promise<void>;
  /** Per-channel recovery hook for a cursor older than retained history. */
  onResync?(error: RealtimeResyncRequiredError): number | Promise<number>;
}

export interface RealtimeConnectionOptions {
  /** Full wss:// endpoint URL, including the endpoint ID. */
  url: string;
  /**
   * Supply a WebSocket constructor that can add the OIDC bearer header.
   * Rejections are retried; throw RealtimeConfigurationError for permanent
   * errors such as a malformed credential.
   */
  webSocketFactory(url: string, protocols: string[]): RealtimeSocket | Promise<RealtimeSocket>;
  signal?: AbortSignal;
  retryInitialMs?: number;
  retryMaxMs?: number;
  /** Maximum wait for socket creation, handshake, or subscription before retrying. */
  connectTimeoutMs?: number;
  /** Reset reconnect backoff after the subscription remains healthy this long. */
  stableConnectionMs?: number;
}

/** Consume one channel over a dedicated v2 WebSocket connection. */
export interface ConsumeRealtimeChannelOptions extends RealtimeConnectionOptions, RealtimeChannelConsumerOptions {}

/** Consume several independent channels over one shared v2 WebSocket. */
export interface ConsumeRealtimeChannelsOptions extends RealtimeConnectionOptions {
  channels: readonly RealtimeChannelConsumerOptions[];
}

/** Throw from a socket factory when retrying cannot fix the error. */
export class RealtimeConfigurationError extends TypeError {
  constructor(message: string) {
    super(message);
    this.name = 'RealtimeConfigurationError';
  }
}

export class RealtimeResyncRequiredError extends Error {
  constructor(
    readonly channel: string,
    readonly oldestSequence: number,
    readonly latestSequence: number,
    readonly afterSequence = 0,
  ) {
    super(`realtime history unavailable for channel ${channel} after sequence ${afterSequence}; rebuild state before resubscribing`);
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

type SocketEvent = { type: 'open' } | { type: 'close' } | { type: 'abort' } | { type: 'timeout' } | { type: 'message'; data: unknown };

function eventsFor(socket: RealtimeSocket, signal: AbortSignal | undefined, connectTimeoutMs: number): {
  next(): Promise<SocketEvent>;
  armTimeout(): void;
  clearTimeout(): void;
  dispose(): void;
} {
  const queued: SocketEvent[] = [];
  let waiter: ((event: SocketEvent) => void) | undefined;
  let connectTimer: ReturnType<typeof setTimeout> | undefined;
  let connected = false;
  const clearConnectTimer = () => {
    if (connectTimer) clearTimeout(connectTimer);
    connectTimer = undefined;
  };
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
  const open = () => {
    if (connected) return;
    connected = true;
    clearConnectTimer();
    push({ type: 'open' });
  };
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
  if (socket.readyState === 1) open();
  if (!connected) connectTimer = setTimeout(() => push({ type: 'timeout' }), connectTimeoutMs);
  if (signal?.aborted) abort();
  return {
    next: () => queued.length ? Promise.resolve(queued.shift()!) : new Promise((resolve) => { waiter = resolve; }),
    armTimeout: () => {
      clearConnectTimer();
      connectTimer = setTimeout(() => push({ type: 'timeout' }), connectTimeoutMs);
    },
    clearTimeout: clearConnectTimer,
    dispose: () => {
      socket.removeEventListener('open', open);
      socket.removeEventListener('message', message);
      socket.removeEventListener('close', close);
      socket.removeEventListener('error', error);
      signal?.removeEventListener('abort', abort);
      clearConnectTimer();
      try { socket.close(); } catch { /* connection is already gone */ }
    },
  };
}

type SocketCreation = { type: 'socket'; socket: RealtimeSocket } | { type: 'timeout' } | { type: 'abort' };

async function createSocket(
  factory: RealtimeConnectionOptions['webSocketFactory'],
  url: string,
  protocols: string[],
  timeoutMs: number,
  signal?: AbortSignal,
): Promise<SocketCreation> {
  if (signal?.aborted) return { type: 'abort' };
  let abandoned = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let abortListener: (() => void) | undefined;
  const opening = Promise.resolve().then(() => factory(url, protocols)).then((socket) => {
    if (abandoned) {
      try { socket.close(); } catch { /* the late socket is already gone */ }
      return { type: 'abort' } as const;
    }
    return { type: 'socket', socket } as const;
  });
  try {
    const result = await Promise.race([
      opening,
      new Promise<SocketCreation>((resolve) => {
        timer = setTimeout(() => resolve({ type: 'timeout' }), timeoutMs);
        if (signal) {
          abortListener = () => resolve({ type: 'abort' });
          signal.addEventListener('abort', abortListener, { once: true });
          if (signal.aborted) abortListener();
        }
      }),
    ]);
    if (result.type !== 'socket') abandoned = true;
    return result;
  } catch (error) {
    abandoned = true;
    throw error;
  } finally {
    if (timer) clearTimeout(timer);
    if (abortListener) signal?.removeEventListener('abort', abortListener);
  }
}

function jitteredDelay(delay: number): number {
  return delay <= 1 ? delay : Math.floor(delay * (0.5 + Math.random() * 0.5));
}

function nextRetryDelay(delay: number, initial: number, maximum: number): number {
  return Math.min(Math.max(delay * 2, initial), maximum);
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
    data: Uint8Array.from(atob(frame.data_base64 ?? ''), (byte) => byte.charCodeAt(0)),
    binary: frame.binary ?? false,
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

function validateChannel(channel: string): void {
  if (!channel || new TextEncoder().encode(channel).byteLength > 256 || channel.trim() !== channel ||
      /[/?#\r\n]/.test(channel)) throw new TypeError('invalid realtime channel');
}

function validateConnectionOptions(options: RealtimeConnectionOptions): { url: string; initialDelay: number; maxDelay: number; connectTimeoutMs: number; stableConnectionMs: number } {
  const url = new URL(options.url);
  if (url.protocol !== 'wss:' && url.protocol !== 'ws:') throw new TypeError('realtime URL must use wss or ws');
  const initialDelay = options.retryInitialMs ?? 100;
  const maxDelay = options.retryMaxMs ?? 5_000;
  const connectTimeoutMs = options.connectTimeoutMs ?? 10_000;
  const stableConnectionMs = options.stableConnectionMs ?? 30_000;
  if (!Number.isFinite(initialDelay) || initialDelay < 0 || !Number.isFinite(maxDelay) || maxDelay < initialDelay) {
    throw new TypeError('invalid realtime retry delay');
  }
  if (!Number.isFinite(connectTimeoutMs) || connectTimeoutMs <= 0 ||
      !Number.isFinite(stableConnectionMs) || stableConnectionMs <= 0) {
    throw new TypeError('invalid realtime connection timeout');
  }
  return { url: url.href, initialDelay, maxDelay, connectTimeoutMs, stableConnectionMs };
}

interface ChannelState {
  options: RealtimeChannelConsumerOptions;
  after: number;
  subscribed: boolean;
}

/**
 * Consume several v2 channels over one WebSocket until aborted. Each channel
 * keeps its own durable cursor and recovery callback. Message handlers run in
 * socket order, so a slow handler applies backpressure to all channels on this
 * connection; a disconnect resumes each channel from its last saved cursor.
 */
export async function consumeRealtimeChannels(options: ConsumeRealtimeChannelsOptions): Promise<void> {
  const config = validateConnectionOptions(options);
  if (!Array.isArray(options.channels) || options.channels.length === 0 ||
      options.channels.length > REALTIME_MAX_CHANNELS_PER_CONNECTION) {
    throw new TypeError(`realtime connection requires between 1 and ${REALTIME_MAX_CHANNELS_PER_CONNECTION} channels`);
  }
  const states = new Map<string, ChannelState>();
  for (const channelOptions of options.channels) {
    if (!channelOptions || typeof channelOptions !== 'object') throw new TypeError('invalid realtime channel options');
    validateChannel(channelOptions.channel);
    if (states.has(channelOptions.channel)) throw new TypeError(`duplicate realtime channel: ${channelOptions.channel}`);
    states.set(channelOptions.channel, { options: channelOptions, after: 0, subscribed: false });
  }
  const loadedCursors = await Promise.all([...states.values()].map((state) => state.options.cursorStore.load()));
  [...states.values()].forEach((state, index) => {
    state.after = sequence(loadedCursors[index], 'stored cursor');
  });

  const { url, initialDelay, maxDelay, connectTimeoutMs, stableConnectionMs } = config;
  let delay = initialDelay;
  while (!options.signal?.aborted) {
    let creation: SocketCreation;
    try {
      creation = await createSocket(options.webSocketFactory, url, [REALTIME_RESUME_SUBPROTOCOL], connectTimeoutMs, options.signal);
    } catch (error) {
      if (options.signal?.aborted) return;
      if (error instanceof RealtimeConfigurationError) throw error;
      await pause(jitteredDelay(delay), options.signal);
      delay = nextRetryDelay(delay, initialDelay, maxDelay);
      continue;
    }
    if (creation.type === 'abort' || options.signal?.aborted) return;
    if (creation.type === 'timeout') {
      await pause(jitteredDelay(delay), options.signal);
      delay = nextRetryDelay(delay, initialDelay, maxDelay);
      continue;
    }
    const socket = creation.socket;
    const events = eventsFor(socket, options.signal, connectTimeoutMs);
    let connected = false;
    let subscribedCount = 0;
    let reconnectRequested = false;
    let stableTimer: ReturnType<typeof setTimeout> | undefined;
    try {
      while (!options.signal?.aborted) {
        const event = await events.next();
        if (event.type === 'abort' || event.type === 'close' || event.type === 'timeout') break;
        if (event.type === 'open') {
          if (connected) continue;
          if (socket.protocol !== REALTIME_RESUME_SUBPROTOCOL) {
            throw new RealtimeProtocolError('realtime v2 subprotocol was not negotiated');
          }
          connected = true;
          try {
            for (const [channel, state] of states) {
              state.subscribed = false;
              socket.send(JSON.stringify({ type: 'subscribe', channel, after: state.after }));
            }
          } catch { throw new SocketSendError(); }
          events.armTimeout();
          continue;
        }
        if (!connected) throw new RealtimeProtocolError('realtime message before WebSocket open');
        const frame = frameOf(event.data);
        if (typeof frame.channel !== 'string') throw new RealtimeProtocolError('realtime frame has no channel');
        const channel = frame.channel;
        const state = states.get(channel);
        if (!state) throw new RealtimeProtocolError('unexpected realtime channel');
        switch (frame.type) {
          case 'subscribed':
            if (state.subscribed || sequence(frame.sequence, 'subscription sequence') !== state.after) {
              throw new RealtimeProtocolError('invalid realtime subscription response');
            }
            state.subscribed = true;
            subscribedCount++;
            if (subscribedCount === states.size) {
              events.clearTimeout();
              stableTimer = setTimeout(() => { delay = initialDelay; }, stableConnectionMs);
            }
            break;
          case 'message': {
            if (!state.subscribed) throw new RealtimeProtocolError('realtime message before subscription');
            const message = decodeMessage(frame, channel, state.after);
            await state.options.onMessage(message);
            await state.options.cursorStore.save(message.sequence);
            state.after = message.sequence;
            try { socket.send(JSON.stringify({ type: 'ack', channel, sequence: state.after })); }
            catch { throw new SocketSendError(); }
            break;
          }
          case 'acknowledged': {
            if (!state.subscribed) throw new RealtimeProtocolError('realtime ack before subscription');
            if (sequence(frame.sequence, 'acknowledged sequence') > state.after) {
              throw new RealtimeProtocolError('realtime acknowledgement exceeds processed cursor');
            }
            break;
          }
          case 'resync_required': {
            const oldestSequence = sequence(frame.oldest_sequence, 'oldest sequence');
            const latestSequence = sequence(frame.latest_sequence, 'latest sequence');
            if (oldestSequence === 0 || oldestSequence - 1 > latestSequence) {
              throw new RealtimeProtocolError('invalid realtime resync bounds');
            }
            const resyncError = new RealtimeResyncRequiredError(
              channel, oldestSequence, latestSequence, state.after);
            if (!state.options.onResync) throw resyncError;
            const recoveredAfter = sequence(await state.options.onResync(resyncError), 'resync cursor');
            if (recoveredAfter < oldestSequence - 1 || recoveredAfter > latestSequence) {
              throw new RealtimeProtocolError('resync handler cursor is outside retained history bounds');
            }
            await state.options.cursorStore.save(recoveredAfter);
            state.after = recoveredAfter;
            reconnectRequested = true;
            break;
          }
          case 'error':
            if (frame.code === 'history_read_failed') break;
            throw new RealtimeProtocolError(`realtime subscription failed: ${String(frame.code)}`);
          default:
            throw new RealtimeProtocolError('unknown realtime frame');
        }
        if (reconnectRequested || (frame.type === 'error' && frame.code === 'history_read_failed')) break;
      }
    } catch (error) {
      if (!(error instanceof SocketSendError)) throw error;
    } finally {
      if (stableTimer) clearTimeout(stableTimer);
      events.dispose();
    }
    if (options.signal?.aborted) return;
    await pause(jitteredDelay(delay), options.signal);
    delay = nextRetryDelay(delay, initialDelay, maxDelay);
  }
}

/**
 * Consume one v2 channel until aborted. The cursor advances only after both
 * `onMessage` and `cursorStore.save` succeed. A disconnect reconnects with
 * the saved cursor; application processing remains at least once.
 *
 * A stale cursor throws `RealtimeResyncRequiredError` unless `onResync`
 * rebuilds application state and returns the sequence represented by it.
 */
export async function consumeRealtimeChannel(options: ConsumeRealtimeChannelOptions): Promise<void> {
  const { channel, cursorStore, onMessage, onResync, ...connection } = options;
  return consumeRealtimeChannels({
    ...connection,
    channels: [{ channel, cursorStore, onMessage, onResync }],
  });
}
