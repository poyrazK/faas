import type { RealtimeMessageExpiryObserver } from './realtime-expiry.js';
import {
  consumeRealtimeChannel, RealtimeConfigurationError, RealtimeProtocolError,
  RealtimeResyncRequiredError, type RealtimeCursorStore, type RealtimeMessage, type RealtimeSocket,
} from './realtime-resume.js';
import { createBrowserRealtimeSocketFactory, createBrowserRealtimeTicketSocketFactory } from './browser-realtime.js';
import { subscribeRealtimeSSE, RealtimeSSEError, type RealtimeSSEMessage } from './realtime-sse.js';

export type RealtimeTransport = 'websocket' | 'sse';
export type RealtimeTransportMessage = Omit<RealtimeMessage, 'messageId'>;
export interface RealtimeTransportOptions {
  onMessageExpired?: RealtimeMessageExpiryObserver;
  /** Managed endpoint URL, HTTP(S) or WS(S). */
  url: string;
  channel: string;
  /** Fresh OIDC token or subscription ticket on every transport connection. */
  token: () => string | Promise<string>;
  cursorStore: RealtimeCursorStore;
  onMessage(message: RealtimeTransportMessage): void | Promise<void>;
  onResync?(error: RealtimeResyncRequiredError): number | Promise<number>;
  onTransportChange?(status: { transport: RealtimeTransport; reason: 'initial' | 'fallback' }): void;
  signal?: AbortSignal;
  fetch?: typeof fetch;
  /** For Node, supply a factory that authenticates its WebSocket handshake. */
  webSocketFactory?(url: string, protocols: string[]): RealtimeSocket | Promise<RealtimeSocket>;
  /** Failed connection/subscription attempts before fallback; default 2. */
  fallbackAfter?: number;
  /** Deadline for each WebSocket connection/subscription attempt; default 5000. */
  connectTimeoutMs?: number;
}

function safeSequence(value: unknown): number {
  const result = typeof value === 'string' && /^(0|[1-9][0-9]*)$/.test(value) ? Number(value) : value;
  if (typeof result !== 'number' || !Number.isSafeInteger(result) || result < 0) throw new RealtimeProtocolError('invalid transport cursor');
  return result;
}
function sseMessage(message: RealtimeSSEMessage): RealtimeTransportMessage {
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(message.data_base64)) throw new RealtimeProtocolError('invalid SSE payload');
  const raw = atob(message.data_base64);
  if (raw.length > 4096) throw new RealtimeProtocolError('SSE payload exceeds realtime bound');
  const metadata = message.metadata ?? undefined;
  if (metadata !== undefined) {
    if (typeof metadata !== 'object' || Array.isArray(metadata) || Object.keys(metadata).length > 8) throw new RealtimeProtocolError('invalid SSE metadata');
    for (const [key, value] of Object.entries(metadata)) {
      if (!/^[a-z0-9_.-]{1,64}$/.test(key) || typeof value !== 'string' || new TextEncoder().encode(value).length > 128 || /[\x00\r\n]/.test(value)) throw new RealtimeProtocolError('invalid SSE metadata');
    }
  }
  if (message.target_message_id != null && typeof message.target_message_id !== 'string') throw new RealtimeProtocolError('invalid SSE message identity');
  const version = message.version ?? 1;
  const event = message.message_event ?? 'created';
  if (!Number.isSafeInteger(version) || version < 1 || !['created', 'updated', 'deleted'].includes(event)
    || (message.deleted !== undefined && typeof message.deleted !== 'boolean')) throw new RealtimeProtocolError('invalid SSE mutation');
  if (message.expires_at != null && (typeof message.expires_at !== "string" || message.expires_at.length > 64 || !Number.isFinite(Date.parse(message.expires_at)))) throw new RealtimeProtocolError("invalid SSE expiry");
  return { expiresAt: message.expires_at ?? undefined, channel: message.channel, sequence: safeSequence(message.sequence),
    data: Uint8Array.from(raw, (character) => character.charCodeAt(0)), binary: message.binary,
    metadata, targetMessageId: message.target_message_id || undefined,
    version, event: event as RealtimeMessage['event'], deleted: message.deleted === true || event === 'deleted' };
}

/** Receive-only. Fallback is sticky for this invocation; processing precedes cursor persistence. */
export async function consumeRealtimeChannelWithFallback(options: RealtimeTransportOptions): Promise<void> {
  const endpoint = new URL(options.url);
  if (!['http:', 'https:', 'ws:', 'wss:'].includes(endpoint.protocol) || endpoint.username || endpoint.password || endpoint.hash) throw new RealtimeConfigurationError('invalid managed realtime URL');
  if (!options.channel || options.channel.trim() !== options.channel || /[\x00\r\n/?#]/.test(options.channel) || new TextEncoder().encode(options.channel).length > 256) throw new RealtimeConfigurationError('invalid channel');
  for (const forbidden of ['filter', 'durableSubscription', 'inbox', 'presence', 'presenceScope', 'onDirectMessage', 'onSignal', 'onReadProgress', 'onReadError', 'onPresence', 'onActivityChange', 'onEphemeralRejected', 'readReceipts', 'directMessages']) {
    if (forbidden in options) throw new RealtimeConfigurationError(`${forbidden} is unsupported by receive-only fallback`);
  }
  const threshold = options.fallbackAfter ?? 2;
  const timeout = options.connectTimeoutMs ?? 5000;
  if (!Number.isInteger(threshold) || threshold < 1 || threshold > 10 || !Number.isInteger(timeout) || timeout < 100 || timeout > 60000) throw new RealtimeConfigurationError('invalid fallback policy');
  let cursor = safeSequence(await options.cursorStore.load());
  if (options.signal?.aborted) return;
  const wsURL = new URL(endpoint); wsURL.protocol = ['https:', 'wss:'].includes(endpoint.protocol) ? 'wss:' : 'ws:';
  wsURL.searchParams.delete('transport'); wsURL.searchParams.delete('channel'); wsURL.searchParams.delete('after');
  const sseURL = new URL(endpoint); sseURL.protocol = ['https:', 'wss:'].includes(endpoint.protocol) ? 'https:' : 'http:';
  const phase = new AbortController();
  const signal = options.signal ? AbortSignal.any([options.signal, phase.signal]) : phase.signal;
  let failures = 0;
  let fallback = false;
  let terminal: Error | undefined;
  const cleanups = new Set<() => void>();
  const failed = () => { if (!signal.aborted && ++failures >= threshold) { fallback = true; phase.abort(); } };
  const persist = async (value: number) => { await options.cursorStore.save(value); cursor = value; };
  const factory = async (url: string, protocols: string[]): Promise<RealtimeSocket> => {
    let socket: RealtimeSocket | undefined;
    let counted = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stable: ReturnType<typeof setTimeout> | undefined;
    const countFailure = () => { if (!counted) { counted = true; failed(); } };
    const cleanup = () => {
      clearTimeout(timer); clearTimeout(stable); cleanups.delete(cleanup);
      signal.removeEventListener('abort', cleanup);
      socket?.removeEventListener('close', closed); socket?.removeEventListener('message', message);
    };
    const closed = (event: unknown) => {
      const code = (event as { code?: number })?.code;
      if (code === 1008 || code === 1003 || code === 1002) {
        terminal = new RealtimeProtocolError('WebSocket authentication, policy or protocol rejected'); phase.abort();
      } else if (code === 1000) counted = true; else countFailure();
      cleanup();
    };
    const message = (event: unknown) => {
      const data = (event as { data?: unknown })?.data;
      if (typeof data !== 'string') return;
      let frame: { type?: string; channel?: string; code?: string };
      try { frame = JSON.parse(data) as typeof frame; } catch { return; }
      if (frame?.type === 'resync_required' && frame.channel === options.channel) counted = true;
      if (frame?.type === 'subscribed' && frame.channel === options.channel) {
        clearTimeout(timer);
        stable = setTimeout(() => { failures = 0; }, 30000);
      }
    };
    cleanups.add(cleanup); signal.addEventListener('abort', cleanup, { once: true });
    timer = setTimeout(() => { countFailure(); socket?.close(); cleanup(); }, timeout);
    try {
      if (options.webSocketFactory) socket = await options.webSocketFactory(url, protocols);
      else {
        let credential: string;
        try { credential = await options.token(); } catch { throw new RealtimeConfigurationError("could not obtain realtime credential"); }
        if (typeof credential !== "string") throw new RealtimeConfigurationError("invalid realtime credential");
        const open = credential.startsWith('grt_') ? createBrowserRealtimeTicketSocketFactory(() => credential) : createBrowserRealtimeSocketFactory(() => credential);
        socket = await open(url, protocols);
      }
      if (signal.aborted || counted) { socket.close(); throw new Error('WebSocket attempt ended'); }
      socket.addEventListener('close', closed); socket.addEventListener('message', message);
      // Ensure cleanup happens when the existing consumer disposes a socket.
      const underlying = socket;
      return { get protocol() { return underlying.protocol; }, get readyState() { return underlying.readyState; },
        addEventListener: (type, listener) => underlying.addEventListener(type, listener),
        removeEventListener: (type, listener) => underlying.removeEventListener(type, listener),
        send: (data) => underlying.send(data), close: () => { countFailure(); cleanup(); underlying.close(); } };
    } catch (error) {
      cleanup();
      if (error instanceof RealtimeConfigurationError) throw error;
      const status = (error as { status?: number; statusCode?: number })?.status ?? (error as { statusCode?: number })?.statusCode;
      if (status === 401 || status === 403) throw new RealtimeConfigurationError('WebSocket authorization rejected');
      countFailure(); throw error;
    }
  };
  options.onTransportChange?.({ transport: 'websocket', reason: 'initial' });
  try {
    await consumeRealtimeChannel({ url: wsURL.toString(), channel: options.channel, webSocketFactory: factory, signal,
      connectTimeoutMs: timeout, cursorStore: { load: () => cursor, save: persist }, onResync: options.onResync, onMessageExpired: options.onMessageExpired,
      onMessage: async ({ messageId: _id, ...message }) => { await options.onMessage(message); },
    });
  } finally { phase.abort(); for (const cleanup of cleanups) cleanup(); }
  if (terminal) throw terminal;
  if (options.signal?.aborted || !fallback) return;
  options.onTransportChange?.({ transport: 'sse', reason: 'fallback' });
  while (!options.signal?.aborted) {
    try {
      for await (const message of subscribeRealtimeSSE({ url: sseURL.toString(), channel: options.channel,
        onMessageExpired: options.onMessageExpired, token: options.token, after: String(cursor), signal: options.signal, fetch: options.fetch })) {
        if (!message.checkpoint) await options.onMessage(sseMessage(message));
        await persist(safeSequence(message.sequence));
      }
      return;
    } catch (error) {
      if (!(error instanceof RealtimeSSEError) || error.code !== 'resync_required') throw error;
      const details = error.details as { oldest_sequence?: unknown; latest_sequence?: unknown };
      const resync = new RealtimeResyncRequiredError(options.channel, safeSequence(details?.oldest_sequence), safeSequence(details?.latest_sequence), cursor);
      if (!options.onResync) throw resync;
      await persist(safeSequence(await options.onResync(resync)));
    }
  }
}
