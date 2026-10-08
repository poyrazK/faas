import { createRealtimeActivityTracker, type RealtimeActivity, type RealtimeActivityTracker } from './realtime-activity.js';

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
  metadata?: Record<string,string>;
  /** Stable publisher ID; channel messages without a key cannot be edited. */
  targetMessageId?: string;
  version: number;
  event: 'created' | 'updated' | 'deleted';
  deleted: boolean;
  channel: string;
  sequence: number;
  messageId: string;
  data: Uint8Array;
  binary: boolean;
}

/** A live-only backend message addressed to this authenticated principal. */
export interface RealtimeDirectMessage {
  messageId?: string;
  data: Uint8Array;
  binary: boolean;
  receiptRequested: boolean;
}

/** A retained principal notification, replayed until this device acknowledges it. */
export interface RealtimeInboxMessage {
  /** Stable publisher ID; channel messages without a key cannot be edited. */
  targetMessageId?: string;
  version: number;
  event: 'created' | 'updated' | 'deleted';
  deleted: boolean;
  messageId: string;
  sequence: number;
  data: Uint8Array;
  binary: boolean;
}

export interface RealtimeReadProgress {
  inbox: boolean;
  channel?: string;
  /** Stable SHA-256 principal key; raw identity claims are never broadcast. */
  readerId: string;
  sequence: number;
  unread: number;
  oldestSequence: number;
  latestSequence: number;
  historyUnavailable: boolean;
}

export interface RealtimeReadError {
  inbox: boolean;
  channel?: string;
  code: string;
}

export interface RealtimeReadActions {
  /** Call only when the person has seen messages through this processed sequence. */
  markRead(sequence: number): void;
  refreshReadProgress(): void;
}

export type RealtimePushRegistration =
  | { provider: 'fcm' | 'apns'; target: { token: string } }
  | { provider: 'webpush'; target: { endpoint: string; p256dh: string; auth: string } };

export interface RealtimeNotificationPreferences {
  /** Shared quota across this principal's devices; null/omitted disables it. */
  rate_limit?: { max_notifications: number; window_seconds: 60 | 300 | 3600; allow_urgent_bypass?: boolean } | null;
  /** Explicitly let urgent alerts bypass quiet hours and digests; defaults to false. */
  allow_urgent_bypass?: boolean;
  /** Fixed UTC delivery windows: immediate, five minutes, or hourly. */
  digest_interval_seconds?: 0 | 300 | 3600;
  /** Defaults to true; combine alerts released after quiet hours. */
  summarize_quiet_hours?: boolean | null;
  enabled: boolean;
  /** Unlisted categories remain enabled. */
  categories?: Record<string, boolean> | null;
  /** null/omitted means all devices; [] means no devices. */
  devices?: string[] | null;
  quiet_hours?: { timezone: string; start: string; end: string } | null;
}

export interface RealtimePushActions {
  /** Registration is confirmed asynchronously by onPushRegistered. */
  registerPush(registration: RealtimePushRegistration): void;
  unregisterPush(): void;
  /** Replace preferences for this verified user across devices. */
  setNotificationPreferences(preferences: RealtimeNotificationPreferences): void;
  refreshNotificationPreferences(): void;
}

/** Convert a browser PushSubscription JSON value; obtaining permission remains app-owned. */
export function realtimeWebPushRegistration(subscription: {
  endpoint?: string; keys?: { p256dh?: string; auth?: string };
}): RealtimePushRegistration {
  if (!subscription.endpoint || !subscription.keys?.p256dh || !subscription.keys.auth) {
    throw new TypeError('invalid browser push subscription');
  }
  return { provider: 'webpush', target: { endpoint: subscription.endpoint,
    p256dh: subscription.keys.p256dh, auth: subscription.keys.auth } };
}

export interface RealtimeInboxConsumerOptions {
  onNotificationPreferences?(preferences: RealtimeNotificationPreferences): void | Promise<void>;
  onNotificationPreferencesError?(code: string): void | Promise<void>;
  onPushActions?(actions: RealtimePushActions): void | Promise<void>;
  onPushRegistered?(registered: boolean): void | Promise<void>;
  onPushError?(code: string): void | Promise<void>;
  onReadActions?(actions: RealtimeReadActions): void | Promise<void>;
  onReadProgress?(progress: RealtimeReadProgress): void | Promise<void>;
  onReadError?(error: RealtimeReadError): void | Promise<void>;
  /** Stable device name. Use distinct names for devices with independent progress. */
  consumerId: string;
  initialSequence?: number;
  /** Resolve after committing the application side effect. Replays are possible. */
  onMessage(message: RealtimeInboxMessage): void | Promise<void>;
  onSubscribed?(acknowledgedSequence: number): void | Promise<void>;
  /** Rebuild application state and return a sequence within the supplied bounds. */
  onResync?(error: RealtimeInboxResyncRequiredError): number | Promise<number>;
}

export interface RealtimePresenceMember {
  memberId: string;
  state: Record<string, unknown>;
  /** Active connections represented by this member across the realtime fleet. */
  connectionCount: number;
}

export interface RealtimePresenceEvent {
  channel: string;
  event: 'snapshot' | 'joined' | 'updated' | 'left';
  /** Snapshot events can arrive in chunks; `complete` marks the final chunk. */
  complete?: boolean;
  members?: RealtimePresenceMember[];
  member?: RealtimePresenceMember;
  memberId?: string;
}

export interface RealtimeSignal {
  /** Present for named, expiring signals. */
  name?: string;
  updatedAt?: string;
  expiresAt?: string;
  channel: string;
  memberId: string;
  data: unknown;
}

export interface RealtimeEphemeralRejection {
  channel: string;
  code: 'presence_rate_limited' | 'signal_rate_limited' | 'signal_relay_unavailable';
}

export interface RealtimeChannelActions extends RealtimeReadActions {
  /** Set the state visible to other authorized v2 subscribers in the channel. */
  updatePresence(state: Record<string, unknown>): void;
  /** Send a transient JSON signal to other authorized v2 subscribers in the channel. */
  sendSignal(data: unknown): void;
  /** Replace a named temporary signal; expiry is assigned by Gregale. */
  sendTemporarySignal(name: string, data: unknown, ttlMs?: number): void;
  /** Clear your named signal on other connected subscribers. */
  clearTemporarySignal(name: string): void;
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
  /** Exact-match AND filter; use a new cursor/subscription identity when changing it. */
  filter?: Record<string,string>;
  onReadProgress?(progress: RealtimeReadProgress): void | Promise<void>;
  onReadError?(error: RealtimeReadError): void | Promise<void>;
  channel: string;
  /** Keep the cursor in this client for the original v2 protocol. */
  cursorStore?: RealtimeCursorStore;
  /** Store this consumer's cursor on Gregale for reconnects across devices and reinstalls. */
  durableSubscription?: string;
  /** Baseline for a new durable subscription; existing server checkpoints take precedence. */
  initialSequence?: number;
  /** Initial ephemeral presence state. Up to 512 UTF-8 bytes. */
  presence?: Record<string, unknown>;
  /** Group presence by the verified principal instead of by WebSocket connection. */
  presenceScope?: 'connection' | 'principal';
  /** Commit the application side effect before this callback resolves. */
  onMessage(message: RealtimeMessage): void | Promise<void>;
  /** Called after each subscribe acknowledgement, including reconnects. */
  onSubscribed?(actions: RealtimeChannelActions): void | Promise<void>;
  /** Receive the initial presence snapshot and subsequent membership changes. */
  onPresence?(event: RealtimePresenceEvent): void | Promise<void>;
  /** Receive transient JSON signals. They are not stored in channel history. */
  onSignal?(signal: RealtimeSignal): void | Promise<void>;
  /** Current named activity; expiry and lifecycle changes call this synchronously. */
  onActivityChange?(activities: RealtimeActivity[]): void;
  /** Bound tracked identities including expiry markers; default 1024, maximum 8192. */
  activityMaxEntries?: number;
  /** Learn when an ephemeral update was rate limited or could not be relayed. */
  onEphemeralRejected?(event: RealtimeEphemeralRejection): void | Promise<void>;
  /** Per-channel recovery hook for a cursor older than retained history. */
  onResync?(error: RealtimeResyncRequiredError): number | Promise<number>;
}

export interface RealtimeConnectionOptions {
  /** Subscribe to the authenticated principal inbox on this same socket. */
  inbox?: RealtimeInboxConsumerOptions;
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
  /** Receive live-only backend messages addressed to this verified principal. */
  onDirectMessage?(message: RealtimeDirectMessage): void | Promise<void>;
}

/** Consume one channel over a dedicated v2 WebSocket connection. */
export interface ConsumeRealtimeChannelOptions extends RealtimeConnectionOptions, RealtimeChannelConsumerOptions {}

/** Consume several independent channels over one shared v2 WebSocket. */
export interface ConsumeRealtimeChannelsOptions extends RealtimeConnectionOptions {
  channels: readonly RealtimeChannelConsumerOptions[];
}

export interface ConsumeRealtimeInboxOptions extends RealtimeConnectionOptions {
  inbox: RealtimeInboxConsumerOptions;
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

export class RealtimeInboxResyncRequiredError extends RealtimeResyncRequiredError {
  constructor(readonly consumerId: string, oldest: number, latest: number, after = 0) {
    super('inbox', oldest, latest, after);
    this.name = 'RealtimeInboxResyncRequiredError';
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

function messageMutation(frame: Record<string, unknown>): { targetMessageId?: string; version: number; event: 'created' | 'updated' | 'deleted'; deleted: boolean } {
  const version = sequence(frame.version ?? 1, 'message version');
  const event = frame.message_event ?? 'created';
  if (version < 1 || (event !== 'created' && event !== 'updated' && event !== 'deleted') ||
      (frame.deleted !== undefined && typeof frame.deleted !== 'boolean') ||
      (frame.target_message_id !== undefined && typeof frame.target_message_id !== 'string')) {
    throw new RealtimeProtocolError('invalid retained message mutation');
  }
  const deleted = frame.deleted === true || event === 'deleted';
  return { targetMessageId: typeof frame.target_message_id === 'string' ? frame.target_message_id : undefined,
    version, event: deleted ? 'deleted' : event, deleted };
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
  validateRealtimeMetadata(frame.metadata);
  return {
    metadata: frame.metadata as Record<string,string> | undefined, ...messageMutation(frame), channel, sequence: next, messageId: frame.message_id,
    data: Uint8Array.from(atob(frame.data_base64 ?? ''), (byte) => byte.charCodeAt(0)),
    binary: frame.binary ?? false,
  };
}

function decodeDirectMessage(frame: Record<string, unknown>): RealtimeDirectMessage {
  const receiptRequested = frame.receipt_requested ?? false;
  if (typeof receiptRequested !== 'boolean' ||
      (frame.message_id !== undefined && (typeof frame.message_id !== 'string' || frame.message_id.length < 1 || frame.message_id.length > 128 ||
       /[^\x21-\x7e]|[/?#]/.test(frame.message_id))) ||
      (receiptRequested && typeof frame.message_id !== 'string') ||
      (frame.data_base64 !== undefined && typeof frame.data_base64 !== 'string') ||
      !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(frame.data_base64 ?? '') ||
      (frame.binary !== undefined && typeof frame.binary !== 'boolean') ||
      (frame.receipt_requested !== undefined && typeof frame.receipt_requested !== 'boolean')) {
    throw new RealtimeProtocolError('invalid realtime direct message');
  }
  const bytes = Uint8Array.from(atob(frame.data_base64 ?? ''), (byte) => byte.charCodeAt(0));
  if (bytes.byteLength > 4 * 1024) throw new RealtimeProtocolError('realtime direct message exceeds 4096 bytes');
  return { messageId: typeof frame.message_id === 'string' ? frame.message_id : undefined, data: bytes, binary: frame.binary ?? false, receiptRequested };
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

function encodePresenceState(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError('realtime presence state must be an object');
  }
  let encoded: string | undefined;
  try { encoded = JSON.stringify(value); } catch { throw new TypeError('realtime presence state must be JSON serializable'); }
  if (encoded === undefined || new TextEncoder().encode(encoded).byteLength > 512) {
    throw new TypeError('realtime presence state exceeds 512 UTF-8 bytes');
  }
  return JSON.parse(encoded) as Record<string, unknown>;
}

function encodeSignalData(value: unknown): string {
  let encoded: string | undefined;
  try { encoded = JSON.stringify(value); } catch { throw new TypeError('realtime signal data must be JSON serializable'); }
  if (encoded === undefined || new TextEncoder().encode(encoded).byteLength > 2048) {
    throw new TypeError('realtime signal data exceeds 2048 UTF-8 bytes');
  }
  return encoded;
}

function objectValue(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function decodeNotificationPreferences(value: unknown): RealtimeNotificationPreferences {
  if (!objectValue(value) || typeof value.enabled !== 'boolean') throw new RealtimeProtocolError('invalid notification preferences');
  const rate = value.rate_limit;
  if (rate !== undefined && rate !== null && (!objectValue(rate) ||
      typeof rate.max_notifications !== 'number' || !Number.isInteger(rate.max_notifications) || (rate.max_notifications as number) < 1 || (rate.max_notifications as number) > 100 ||
      (rate.window_seconds !== 60 && rate.window_seconds !== 300 && rate.window_seconds !== 3600) ||
      (rate.allow_urgent_bypass !== undefined && typeof rate.allow_urgent_bypass !== 'boolean'))) {
    throw new RealtimeProtocolError('invalid notification rate limit');
  }
  const urgent = value.allow_urgent_bypass;
  if (urgent !== undefined && typeof urgent !== 'boolean') throw new RealtimeProtocolError('invalid urgent bypass preference');
  const interval = value.digest_interval_seconds;
  if (interval !== undefined && interval !== 0 && interval !== 300 && interval !== 3600) throw new RealtimeProtocolError('invalid digest interval');
  const summary = value.summarize_quiet_hours;
  if (summary !== undefined && summary !== null && typeof summary !== 'boolean') throw new RealtimeProtocolError('invalid quiet-hour digest preference');
  const categories = value.categories;
  if (categories !== undefined && categories !== null && (!objectValue(categories) || Object.keys(categories).length > 32 ||
      Object.entries(categories).some(([key, enabled]) => !/^[a-z0-9_.-]{1,64}$/.test(key) || typeof enabled !== 'boolean'))) {
    throw new RealtimeProtocolError('invalid notification categories');
  }
  const devices = value.devices;
  if (devices !== undefined && devices !== null && (!Array.isArray(devices) || devices.length > 16 ||
      devices.some(device => typeof device !== 'string' || device.trim() !== device || !device || new TextEncoder().encode(device).byteLength > 128 || /[\0\r\n]/.test(device)) ||
      new Set(devices).size !== devices.length)) throw new RealtimeProtocolError('invalid notification devices');
  const quiet = value.quiet_hours;
  if (quiet !== undefined && quiet !== null && (!objectValue(quiet) || typeof quiet.timezone !== 'string' || !quiet.timezone || quiet.timezone === 'Local' || quiet.timezone.length > 128 ||
      typeof quiet.start !== 'string' || typeof quiet.end !== 'string' || !/^(?:[01][0-9]|2[0-3]):[0-5][0-9]$/.test(quiet.start) ||
      !/^(?:[01][0-9]|2[0-3]):[0-5][0-9]$/.test(quiet.end) || quiet.start === quiet.end)) throw new RealtimeProtocolError('invalid quiet hours');
  return { enabled: value.enabled, rate_limit: (rate ?? null) as RealtimeNotificationPreferences['rate_limit'], allow_urgent_bypass: urgent ?? false, digest_interval_seconds: (interval ?? 0) as 0 | 300 | 3600, summarize_quiet_hours: summary ?? true, categories: (categories ?? null) as Record<string, boolean> | null,
    devices: (devices ?? null) as string[] | null, quiet_hours: (quiet ?? null) as RealtimeNotificationPreferences['quiet_hours'] };
}

function decodePresence(frame: Record<string, unknown>, channel: string): RealtimePresenceEvent {
  const connectionCount = (value: unknown): number => {
    if (value === undefined) return 1;
    if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 1 || value > 512) {
      throw new RealtimeProtocolError('invalid realtime presence connection count');
    }
    return value;
  };
  const event = frame.event;
  if (event === 'snapshot') {
    if ((frame.members !== undefined && !Array.isArray(frame.members)) ||
        (frame.complete !== undefined && typeof frame.complete !== 'boolean')) {
      throw new RealtimeProtocolError('invalid realtime presence snapshot');
    }
    const members = (Array.isArray(frame.members) ? frame.members : []).map((value) => {
      if (!objectValue(value) || typeof value.member_id !== 'string' || !value.member_id || !objectValue(value.state)) {
        throw new RealtimeProtocolError('invalid realtime presence member');
      }
      return { memberId: value.member_id, state: value.state, connectionCount: connectionCount(value.connection_count) };
    });
    return { channel, event, complete: frame.complete === true, members };
  }
  if (event === 'left') {
    if (typeof frame.member_id !== 'string' || !frame.member_id) throw new RealtimeProtocolError('invalid realtime presence leave');
    return { channel, event, memberId: frame.member_id };
  }
  if (event === 'joined' || event === 'updated') {
    if (typeof frame.member_id !== 'string' || !frame.member_id || !objectValue(frame.state)) {
      throw new RealtimeProtocolError('invalid realtime presence update');
    }
    return { channel, event, member: {
      memberId: frame.member_id, state: frame.state, connectionCount: connectionCount(frame.connection_count),
    } };
  }
  throw new RealtimeProtocolError('unknown realtime presence event');
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
  pendingReset?: number;
  socket?: RealtimeSocket;
  presenceState?: Record<string, unknown>;
  actions: RealtimeChannelActions;
  activity?: RealtimeActivityTracker;
}

/**
 * Consume several v2 channels over one WebSocket until aborted. Each channel
 * uses either a client-held cursor or a named server-managed subscription.
 * Message handlers run in socket order, so a slow handler applies backpressure
 * to all channels on this connection.
 */
export async function consumeRealtimeChannels(options: ConsumeRealtimeChannelsOptions): Promise<void> {
  const config = validateConnectionOptions(options);
  const inbox = options.inbox;
  if (inbox !== undefined && (!inbox || typeof inbox.consumerId !== 'string' ||
      !inbox.consumerId || new TextEncoder().encode(inbox.consumerId).byteLength > 128 ||
      inbox.consumerId.trim() !== inbox.consumerId || /[\0\r\n]/.test(inbox.consumerId) ||
      typeof inbox.onMessage !== 'function' || (inbox.initialSequence !== undefined &&
      (!Number.isSafeInteger(inbox.initialSequence) || inbox.initialSequence < 0)))) {
    throw new TypeError('invalid realtime inbox consumer');
  }
  if (!Array.isArray(options.channels) || (options.channels.length === 0 && !inbox) ||
      options.channels.length + (inbox ? 1 : 0) > REALTIME_MAX_CHANNELS_PER_CONNECTION) {
    throw new TypeError(`realtime connection allows up to ${REALTIME_MAX_CHANNELS_PER_CONNECTION} subscriptions, including the inbox`);
  }
  let inboxAfter = inbox?.initialSequence ?? 0;
  const readWatermarks = new Map<string, { sequence: number; latest: number; oldest: number }>();
  const states = new Map<string, ChannelState>();
  for (const channelOptions of options.channels) {
    if (!channelOptions || typeof channelOptions !== 'object') throw new TypeError('invalid realtime channel options');
    validateChannel(channelOptions.channel);
 validateRealtimeMetadata(channelOptions.filter);
    if (channelOptions.presenceScope !== undefined && channelOptions.presenceScope !== 'connection' &&
        channelOptions.presenceScope !== 'principal') {
      throw new TypeError('presenceScope must be connection or principal');
    }
    const durableSubscription = channelOptions.durableSubscription;
    if (durableSubscription !== undefined && (typeof durableSubscription !== 'string' || !durableSubscription ||
        new TextEncoder().encode(durableSubscription).byteLength > 128 || durableSubscription.trim() !== durableSubscription ||
        /[\0\r\n]/.test(durableSubscription))) {
      throw new TypeError('invalid realtime durable subscription name');
    }
    if ((durableSubscription === undefined) === (channelOptions.cursorStore === undefined)) {
      throw new TypeError('provide exactly one of cursorStore or durableSubscription');
    }
    if (channelOptions.initialSequence !== undefined &&
        (durableSubscription === undefined || !Number.isSafeInteger(channelOptions.initialSequence) || channelOptions.initialSequence < 0)) {
      throw new TypeError('initialSequence requires a durable subscription and must be a non-negative safe integer');
    }
    if (states.has(channelOptions.channel)) throw new TypeError(`duplicate realtime channel: ${channelOptions.channel}`);
    if (channelOptions.onActivityChange !== undefined && typeof channelOptions.onActivityChange !== 'function') throw new TypeError('onActivityChange must be a function');
    if (channelOptions.activityMaxEntries !== undefined && (!channelOptions.onActivityChange || !Number.isInteger(channelOptions.activityMaxEntries) || channelOptions.activityMaxEntries < 1 || channelOptions.activityMaxEntries > 8192)) throw new TypeError('activityMaxEntries requires onActivityChange and must be 1..8192');
    const state: ChannelState = {
      options: channelOptions, after: 0, subscribed: false,
      activity: channelOptions.onActivityChange === undefined ? undefined : createRealtimeActivityTracker({ onChange: channelOptions.onActivityChange, maxEntries: channelOptions.activityMaxEntries }),
      presenceState: channelOptions.presence === undefined ? undefined : encodePresenceState(channelOptions.presence),
      actions: undefined as unknown as RealtimeChannelActions,
    };
    state.actions = {
      markRead(value) {
        const seen = sequence(value, 'read sequence');
        if (seen > state.after) throw new TypeError('read sequence exceeds processed messages');
        const socket = state.socket;
        if (!state.subscribed || socket?.readyState !== 1) throw new RealtimeProtocolError('realtime channel is not connected');
        socket.send(JSON.stringify({ type: 'read', channel: channelOptions.channel, sequence: seen }));
      },
      refreshReadProgress() {
        const socket = state.socket;
        if (!state.subscribed || socket?.readyState !== 1) throw new RealtimeProtocolError('realtime channel is not connected');
        socket.send(JSON.stringify({ type: 'read_state', channel: channelOptions.channel }));
      },
      updatePresence(next) {
        state.presenceState = encodePresenceState(next);
        const socket = state.socket;
        if (state.subscribed && socket?.readyState === 1) {
          socket.send(JSON.stringify({ type: 'presence', channel: channelOptions.channel, state: state.presenceState }));
        }
      },
      sendSignal(data) {
        const socket = state.socket;
        if (!state.subscribed || socket?.readyState !== 1) {
          throw new RealtimeProtocolError('realtime channel is not connected');
        }
        const encoded = encodeSignalData(data);
        socket.send(JSON.stringify({ type: 'signal', channel: channelOptions.channel, data: JSON.parse(encoded) as unknown }));
      },
      sendTemporarySignal(name, data, ttlMs = 5000) {
        if (!/^[A-Za-z0-9_.:-]{1,64}$/.test(name) || !Number.isInteger(ttlMs) || ttlMs < 0 || ttlMs > 30000) {
          throw new TypeError('temporary signal requires a valid name and ttlMs between 0 and 30000');
        }
        const socket = state.socket;
        if (!state.subscribed || socket?.readyState !== 1) throw new RealtimeProtocolError('realtime channel is not connected');
        const encoded = encodeSignalData(data);
        socket.send(JSON.stringify({ type: 'signal', channel: channelOptions.channel, name, ttl_ms: ttlMs, data: JSON.parse(encoded) as unknown }));
      },
      clearTemporarySignal(name) {
        state.actions.sendTemporarySignal(name, null, 0);
      },
    };
    states.set(channelOptions.channel, state);
  }
  const loadedCursors = await Promise.all([...states.values()].map((state) =>
    state.options.cursorStore?.load() ?? state.options.initialSequence ?? 0));
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
    let inboxSubscribed = false;
    const inboxPushActions: RealtimePushActions = {
      setNotificationPreferences(preferences) {
        if (!inbox || !inboxSubscribed || socket.readyState !== 1) throw new RealtimeProtocolError('realtime inbox is not connected');
        const checked = decodeNotificationPreferences(preferences);
        const wire = JSON.stringify({ type: 'inbox_preferences_put', consumer: inbox.consumerId, data: checked });
        if (new TextEncoder().encode(wire).byteLength > 4096) throw new TypeError('notification preferences are too large');
        socket.send(wire);
      },
      refreshNotificationPreferences() {
        if (!inbox || !inboxSubscribed || socket.readyState !== 1) throw new RealtimeProtocolError('realtime inbox is not connected');
        socket.send(JSON.stringify({ type: 'inbox_preferences_get', consumer: inbox.consumerId }));
      },
      registerPush(registration) {
        if (!inbox || !inboxSubscribed || socket.readyState !== 1) throw new RealtimeProtocolError('realtime inbox is not connected');
        if (!registration || !['fcm', 'apns', 'webpush'].includes(registration.provider)) throw new TypeError('invalid push provider');
        const wire = JSON.stringify({ type: 'inbox_push_register', consumer: inbox.consumerId, data: registration });
        if (new TextEncoder().encode(wire).byteLength > 4096) throw new TypeError('push registration is too large');
        socket.send(wire);
      },
      unregisterPush() {
        if (!inbox || !inboxSubscribed || socket.readyState !== 1) throw new RealtimeProtocolError('realtime inbox is not connected');
        socket.send(JSON.stringify({ type: 'inbox_push_unregister', consumer: inbox.consumerId }));
      },
    };
    const inboxReadActions: RealtimeReadActions = {
      markRead(value) {
        const seen = sequence(value, 'read sequence');
        if (seen > inboxAfter) throw new TypeError('read sequence exceeds processed inbox messages');
        if (!inboxSubscribed || socket.readyState !== 1) throw new RealtimeProtocolError('realtime inbox is not connected');
        socket.send(JSON.stringify({ type: 'inbox_read', sequence: seen }));
      },
      refreshReadProgress() {
        if (!inboxSubscribed || socket.readyState !== 1) throw new RealtimeProtocolError('realtime inbox is not connected');
        socket.send(JSON.stringify({ type: 'inbox_read_state' }));
      },
    };
    let inboxPendingReset: number | undefined;
    let reconnectRequested = false;
    let stableTimer: ReturnType<typeof setTimeout> | undefined;
    const directAckRetries = new Map<string, number>();
    try {
      socketLoop: while (!options.signal?.aborted) {
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
              state.socket = socket;
              state.pendingReset = undefined;
              const subscription = state.options.durableSubscription;
              const presenceOptions = {
                ...((state.options.onReadProgress || state.options.onReadError) ? { read_receipts: true } : {}),
                ...(state.presenceState === undefined ? {} : { state: state.presenceState }),
                ...(state.options.presenceScope === undefined ? {} : { presence_scope: state.options.presenceScope }),
              };
              const directMessageOption = options.onDirectMessage === undefined ? {} : { direct_messages: true };
              socket.send(JSON.stringify(subscription === undefined
                ? { type: 'subscribe', channel, after: state.after, filter: state.options.filter, ...presenceOptions, ...directMessageOption }
                : { type: 'subscribe', channel, subscription,
                    ...(state.options.initialSequence === undefined ? {} : { after: state.options.initialSequence }),
                    filter: state.options.filter, ...presenceOptions, ...directMessageOption }));
            }
            if (inbox) socket.send(JSON.stringify({ type: 'inbox_subscribe', consumer: inbox.consumerId,
              ...((inbox.onReadProgress || inbox.onReadActions || inbox.onReadError) ? { read_receipts: true } : {}),
              after: inbox.initialSequence ?? 0, ...(options.onDirectMessage ? { direct_messages: true } : {}) }));
          } catch { throw new SocketSendError(); }
          events.armTimeout();
          continue;
        }
        if (!connected) throw new RealtimeProtocolError('realtime message before WebSocket open');
        const frame = frameOf(event.data);
        if (frame.type === 'read_receipt' || frame.type === 'read_error') {
          if (frame.inbox !== undefined && typeof frame.inbox !== 'boolean') throw new RealtimeProtocolError('invalid read receipt scope');
          const isInbox = frame.inbox === true;
          const readChannel = typeof frame.channel === 'string' ? frame.channel : undefined;
          const channelState = readChannel === undefined ? undefined : states.get(readChannel);
          if (isInbox ? !inbox || !inboxSubscribed : !channelState?.subscribed) throw new RealtimeProtocolError('unexpected read receipt scope');
          if (frame.type === 'read_error') {
            if (typeof frame.code !== 'string') throw new RealtimeProtocolError('invalid read progress error');
            const error: RealtimeReadError = { inbox: isInbox, channel: readChannel, code: frame.code };
            if (isInbox) await inbox?.onReadError?.(error); else await channelState?.options.onReadError?.(error);
          } else {
            const seen = sequence(frame.sequence ?? 0, 'read sequence');
            const latest = sequence(frame.latest_sequence ?? 0, 'read latest sequence');
            const oldest = sequence(frame.oldest_sequence, 'read oldest sequence');
            const unread = sequence(frame.unread ?? 0, 'unread count');
            if (typeof frame.member_id !== 'string' || !/^[0-9a-f]{64}$/.test(frame.member_id) || seen > latest ||
                oldest < 1 || oldest > latest + 1 || (frame.history_unavailable !== undefined && typeof frame.history_unavailable !== 'boolean')) {
              throw new RealtimeProtocolError('invalid read progress');
            }
            const key = JSON.stringify([isInbox, readChannel, frame.member_id]);
            const previous = readWatermarks.get(key);
            if (!previous || seen >= previous.sequence && latest >= previous.latest && oldest >= previous.oldest) {
              readWatermarks.set(key, { sequence: seen, latest, oldest });
              const progress: RealtimeReadProgress = { inbox: isInbox, channel: readChannel, readerId: frame.member_id,
                sequence: seen, unread, oldestSequence: oldest, latestSequence: latest, historyUnavailable: frame.history_unavailable ?? false };
              if (isInbox) await inbox?.onReadProgress?.(progress); else await channelState?.options.onReadProgress?.(progress);
            }
          }
          continue;
        }
        if (typeof frame.type === 'string' && frame.type.startsWith('inbox_')) {
          if (!inbox || frame.consumer !== inbox.consumerId) {
            throw new RealtimeProtocolError('unexpected realtime inbox consumer');
          }
          switch (frame.type) {
            case 'inbox_subscribed':
              if (inboxSubscribed) throw new RealtimeProtocolError('duplicate inbox subscription');
              inboxAfter = sequence(frame.sequence ?? 0, 'inbox checkpoint');
              inboxSubscribed = true;
              await inbox.onSubscribed?.(inboxAfter);
              await inbox.onPushActions?.(inboxPushActions);
              if (inbox.onNotificationPreferences) inboxPushActions.refreshNotificationPreferences();
              await inbox.onReadActions?.(inboxReadActions);
              if (inbox.onReadProgress) inboxReadActions.refreshReadProgress();
              if (subscribedCount === states.size) {
                events.clearTimeout();
                stableTimer = setTimeout(() => { delay = initialDelay; }, stableConnectionMs);
              }
              break;
            case 'inbox_preferences':
              if (!inboxSubscribed) throw new RealtimeProtocolError('preferences before subscription');
              await inbox.onNotificationPreferences?.(decodeNotificationPreferences(frame.preferences));
              break;
            case 'inbox_preferences_error':
              if (!inboxSubscribed || typeof frame.code !== 'string') throw new RealtimeProtocolError('invalid notification preferences error');
              await inbox.onNotificationPreferencesError?.(frame.code);
              break;
            case 'inbox_push_registered':
            case 'inbox_push_unregistered':
              if (!inboxSubscribed) throw new RealtimeProtocolError('push confirmation before subscription');
              await inbox.onPushRegistered?.(frame.type === 'inbox_push_registered');
              break;
            case 'inbox_push_error':
              if (!inboxSubscribed || typeof frame.code !== 'string') throw new RealtimeProtocolError('invalid push registration error');
              await inbox.onPushError?.(frame.code);
              break;
            case 'inbox_message': {
              if (!inboxSubscribed) throw new RealtimeProtocolError('inbox message before subscription');
              const next = sequence(frame.sequence, 'inbox sequence');
              if (next !== inboxAfter + 1) throw new RealtimeProtocolError('invalid inbox sequence');
              const message = decodeDirectMessage({ ...frame, receipt_requested: true });
              if (!message.messageId) throw new RealtimeProtocolError('inbox message has no ID');
              await inbox.onMessage({ ...messageMutation(frame), messageId: message.messageId, sequence: next, data: message.data, binary: message.binary });
              inboxAfter = next;
              try { socket.send(JSON.stringify({ type: 'inbox_ack', consumer: inbox.consumerId, sequence: next })); }
              catch { throw new SocketSendError(); }
              break;
            }
            case 'inbox_acknowledged':
              if (!inboxSubscribed || sequence(frame.sequence ?? 0, 'inbox acknowledged sequence') > inboxAfter) {
                throw new RealtimeProtocolError('invalid inbox acknowledgement');
              }
              break;
            case 'inbox_resync_required': {
              const oldest = sequence(frame.oldest_sequence, 'inbox oldest sequence');
              const latest = sequence(frame.latest_sequence ?? 0, 'inbox latest sequence');
              if (oldest === 0 || oldest - 1 > latest) throw new RealtimeProtocolError('invalid inbox resync bounds');
              const error = new RealtimeInboxResyncRequiredError(inbox.consumerId, oldest, latest, inboxAfter);
              if (!inbox.onResync) throw error;
              const recovered = sequence(await inbox.onResync(error), 'inbox resync cursor');
              if (recovered < oldest - 1 || recovered > latest) throw new RealtimeProtocolError('inbox resync cursor is outside retained bounds');
              inboxPendingReset = recovered;
              try { socket.send(JSON.stringify({ type: 'inbox_reset', consumer: inbox.consumerId, sequence: recovered })); }
              catch { throw new SocketSendError(); }
              break;
            }
            case 'inbox_reset':
              if (inboxPendingReset !== sequence(frame.sequence ?? 0, 'inbox reset sequence')) {
                throw new RealtimeProtocolError('unexpected inbox reset');
              }
              break socketLoop;
            case 'inbox_error':
              if (frame.code === 'history_read_failed' || frame.code === 'checkpoint_write_failed') break socketLoop;
              throw new RealtimeProtocolError(`realtime inbox failed: ${String(frame.code)}`);
            default:
              throw new RealtimeProtocolError('unknown realtime inbox frame');
          }
          continue;
        }
        if (frame.type === 'direct_message') {
          const message = decodeDirectMessage(frame);
          await options.onDirectMessage?.(message);
          if (message.receiptRequested && message.messageId && options.onDirectMessage) {
            directAckRetries.set(message.messageId, 0);
            try { socket.send(JSON.stringify({ type: 'direct_message_ack', message_id: message.messageId })); }
            catch { throw new SocketSendError(); }
          }
          continue;
        }
        if (frame.type === 'direct_message_acknowledged') {
          if (typeof frame.message_id === 'string') directAckRetries.delete(frame.message_id);
          continue;
        }
        if (frame.type === 'direct_message_ack_failed') {
          if (typeof frame.message_id !== 'string') throw new RealtimeProtocolError('invalid realtime direct message acknowledgement response');
          const retries = directAckRetries.get(frame.message_id);
          if (retries !== undefined && retries < 2) {
            directAckRetries.set(frame.message_id, retries + 1);
            try { socket.send(JSON.stringify({ type: 'direct_message_ack', message_id: frame.message_id })); }
            catch { throw new SocketSendError(); }
          } else {
            directAckRetries.delete(frame.message_id);
          }
          continue;
        }
        if (typeof frame.channel !== 'string') throw new RealtimeProtocolError('realtime frame has no channel');
        const channel = frame.channel;
        const state = states.get(channel);
        if (!state) throw new RealtimeProtocolError('unexpected realtime channel');
        switch (frame.type) {
          case 'subscribed':
            if (state.subscribed) {
              throw new RealtimeProtocolError('invalid realtime subscription response');
            }
            if (state.options.durableSubscription === undefined) {
              if (sequence(frame.sequence ?? 0, 'subscription sequence') !== state.after) {
                throw new RealtimeProtocolError('invalid realtime subscription response');
              }
            } else {
              if (frame.subscription !== state.options.durableSubscription) {
                throw new RealtimeProtocolError('invalid realtime durable subscription response');
              }
              state.after = sequence(frame.sequence ?? 0, 'subscription sequence');
            }
            state.subscribed = true;
            subscribedCount++;
            await state.options.onSubscribed?.(state.actions);
            if (state.options.onReadProgress) state.actions.refreshReadProgress();
            if (subscribedCount === states.size && (!inbox || inboxSubscribed)) {
              events.clearTimeout();
              stableTimer = setTimeout(() => { delay = initialDelay; }, stableConnectionMs);
            }
            break;
          case 'unsubscribed': {
            if (!state.subscribed) throw new RealtimeProtocolError('unexpected realtime unsubscribe acknowledgement');
            state.subscribed = false;
            state.activity?.reset(channel);
            subscribedCount--;
            break;
          }
          case 'checkpoint': {
            if (!state.subscribed || !state.options.filter || Object.keys(state.options.filter).length === 0) throw new RealtimeProtocolError('unexpected filter checkpoint');
            const next = sequence(frame.sequence, 'checkpoint sequence');
            if (next !== state.after + 1) throw new RealtimeProtocolError('filter checkpoint gap');
            await state.options.cursorStore?.save(next);
            state.after = next;
            try { socket.send(JSON.stringify({ type: 'ack', channel, sequence: next })); }
            catch { throw new SocketSendError(); }
            break;
          }
          case 'message': {
            if (!state.subscribed) throw new RealtimeProtocolError('realtime message before subscription');
            const message = decodeMessage(frame, channel, state.after);
            await state.options.onMessage(message);
            if (state.options.cursorStore) await state.options.cursorStore.save(message.sequence);
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
            if (state.options.durableSubscription !== undefined && frame.subscription !== state.options.durableSubscription) {
              throw new RealtimeProtocolError('invalid realtime durable acknowledgement');
            }
            break;
          }
          case 'presence': {
            if (!state.subscribed) throw new RealtimeProtocolError('realtime presence before subscription');
            const presence = decodePresence(frame, channel);
            if (presence.event === 'left' && presence.memberId !== undefined) state.activity?.removeMember(channel, presence.memberId);
            await state.options.onPresence?.(presence);
            break;
          }
          case 'signal': {
            if (!state.subscribed || typeof frame.member_id !== 'string' || !frame.member_id || !('data' in frame)) {
              throw new RealtimeProtocolError('invalid realtime signal');
            }
            const signal: RealtimeSignal = { channel, memberId: frame.member_id, data: frame.data };
            if (frame.name !== undefined || frame.expires_at !== undefined) {
              if (typeof frame.name !== 'string' || !/^[A-Za-z0-9_.:-]{1,64}$/.test(frame.name) ||
                  typeof frame.updated_at !== 'string' || typeof frame.expires_at !== 'string') {
                throw new RealtimeProtocolError('invalid temporary realtime signal');
              }
              const updated = Date.parse(frame.updated_at), expires = Date.parse(frame.expires_at);
              if (!Number.isFinite(updated) || !Number.isFinite(expires) || expires < updated || expires - updated > 30000) {
                throw new RealtimeProtocolError('invalid temporary realtime signal expiry');
              }
              signal.name = frame.name;
              signal.updatedAt = frame.updated_at;
              signal.expiresAt = frame.expires_at;
            }
            state.activity?.apply(signal);
            await state.options.onSignal?.(signal);
            break;
          }
          case 'resync_required': {
            state.activity?.reset(channel);
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
            if (state.options.durableSubscription !== undefined) {
              if (frame.subscription !== state.options.durableSubscription) {
                throw new RealtimeProtocolError('invalid realtime durable resync request');
              }
              state.pendingReset = recoveredAfter;
              try {
                socket.send(JSON.stringify({ type: 'reset', channel,
                  subscription: state.options.durableSubscription, sequence: recoveredAfter }));
              } catch { throw new SocketSendError(); }
            } else {
              await state.options.cursorStore!.save(recoveredAfter);
              state.after = recoveredAfter;
              reconnectRequested = true;
            }
            break;
          }
          case 'subscription_reset': {
            const resetSequence = sequence(frame.sequence ?? 0, 'reset sequence');
            if (state.options.durableSubscription === undefined ||
                frame.subscription !== state.options.durableSubscription || state.pendingReset !== resetSequence) {
              throw new RealtimeProtocolError('unexpected realtime durable cursor reset');
            }
            state.after = resetSequence;
            state.pendingReset = undefined;
            reconnectRequested = true;
            break;
          }
          case 'error':
            if (frame.code === 'history_read_failed' || frame.code === 'checkpoint_write_failed') break;
            if (frame.code === 'presence_rate_limited' || frame.code === 'signal_rate_limited' ||
                frame.code === 'signal_relay_unavailable') {
              await state.options.onEphemeralRejected?.({ channel, code: frame.code });
              break;
            }
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
      let activityCleanupError: unknown;
      let activityCleanupFailed = false;
      for (const state of states.values()) {
        state.subscribed = false;
        state.socket = undefined;
        try { state.activity?.reset(state.options.channel); }
        catch (error) { if (!activityCleanupFailed) activityCleanupError = error; activityCleanupFailed = true; }
      }
      if (activityCleanupFailed) throw activityCleanupError;
    }
    if (options.signal?.aborted) return;
    await pause(jitteredDelay(delay), options.signal);
    delay = nextRetryDelay(delay, initialDelay, maxDelay);
  }
}

/**
 * Consume one v2 channel until aborted. For client-held cursors, the cursor
 * advances only after both `onMessage` and `cursorStore.save` succeed. For a
 * named server-managed subscription, Gregale persists the ack after
 * `onMessage`. Application processing remains at least once in both modes.
 *
 * A stale cursor throws `RealtimeResyncRequiredError` unless `onResync`
 * rebuilds application state and returns the sequence represented by it.
 */
export async function consumeRealtimeChannel(options: ConsumeRealtimeChannelOptions): Promise<void> {
  const { channel, filter, cursorStore, durableSubscription, initialSequence, presence, presenceScope, onMessage, onSubscribed, onPresence, onSignal, onActivityChange, activityMaxEntries, onEphemeralRejected, onReadProgress, onReadError, onResync, ...connection } = options;
  return consumeRealtimeChannels({
    ...connection,
    channels: [{ channel, filter, cursorStore, durableSubscription, initialSequence, presence, presenceScope, onMessage, onSubscribed, onPresence, onSignal, onActivityChange, activityMaxEntries, onEphemeralRejected, onReadProgress, onReadError, onResync }],
  });
}

/** Consume a device inbox over a dedicated authenticated v2 WebSocket. */
export async function consumeRealtimeInbox(options: ConsumeRealtimeInboxOptions): Promise<void> {
  return consumeRealtimeChannels({ ...options, channels: [] });
}

export interface RealtimeChannelSnapshot {
  entity_versions?: Record<string, number>;
  entity_expirations?: Record<string, string>;
  channel: string;
  sequence: number;
  resume_after_sequence: number;
  data: Uint8Array;
  binary: boolean;
  updated_at: string;
  expires_at: string;
}
/** Load through your authorized backend, commit state, then return the replay baseline. */
export async function recoverRealtimeChannelSnapshot(
  channel: string,
  load: () => Promise<unknown>,
  apply: (snapshot: RealtimeChannelSnapshot) => void | Promise<void>,
): Promise<number> {
  const value = await load();
  if (!objectValue(value) || value.channel !== channel || typeof value.data_base64 !== 'string' ||
      value.data_base64.length > 87384 || typeof value.binary !== 'boolean' ||
      typeof value.updated_at !== 'string' || !Number.isFinite(Date.parse(value.updated_at)) ||
      typeof value.expires_at !== 'string' || !Number.isFinite(Date.parse(value.expires_at)) || Date.parse(value.expires_at) <= Date.now()) {
    throw new RealtimeProtocolError('invalid or expired channel snapshot');
  }
  const baseline = sequence(value.sequence, 'snapshot sequence');
  if (sequence(value.resume_after_sequence, 'snapshot cursor') !== baseline) throw new RealtimeProtocolError('snapshot cursor mismatch');
  let entityVersions: Record<string, number> | undefined;
  if (value.entity_versions !== undefined) {
    if (!objectValue(value.entity_versions) || Object.keys(value.entity_versions).length > 256 ||
        Object.entries(value.entity_versions).some(([key, version]) => !key || key.trim() !== key || new TextEncoder().encode(key).byteLength > 128 || /[\0\r\n]/.test(key) || typeof version !== 'number' || !Number.isSafeInteger(version) || version < 1)) {
      throw new RealtimeProtocolError('invalid snapshot entity versions');
    }
    entityVersions = { ...value.entity_versions } as Record<string, number>;
  }
  let entityExpirations: Record<string, string> | undefined;
  if (value.entity_expirations !== undefined) {
    if (!objectValue(value.entity_expirations) || Object.keys(value.entity_expirations).length > 128 ||
        Object.entries(value.entity_expirations).some(([key, deadline]) => !key || key.trim() !== key || new TextEncoder().encode(key).byteLength > 128 || /[\0\r\n]/.test(key) || !realtimeReducerDeadlineValid(deadline))) {
      throw new RealtimeProtocolError('invalid snapshot entity expirations');
    }
    entityExpirations = { ...value.entity_expirations } as Record<string, string>;
  }
  let decoded: string;
  try { decoded = atob(value.data_base64); } catch { throw new RealtimeProtocolError('invalid snapshot base64'); }
  if (decoded.length > 65536) throw new RealtimeProtocolError('snapshot exceeds size limit');
  await apply({ channel, sequence: baseline, resume_after_sequence: baseline, entity_versions: entityVersions, entity_expirations: entityExpirations,
    data: Uint8Array.from(decoded, character => character.charCodeAt(0)), binary: value.binary,
    updated_at: value.updated_at, expires_at: value.expires_at });
  return baseline;
}

export interface RealtimeBatchRequest {
  expected_sequence?: number;
  batch_id: string;
  messages: Array<{ data_base64: string; binary: boolean; metadata?: Record<string,string> }>;
}
export interface RealtimeBatchResult {
  batch_id: string;
  durable: true;
  partial: boolean;
  messages: Array<{ sequence: number; durable: true; partial: boolean }>;
}
/** Publish via an authorized backend transport; reuse the batch ID when retrying. */
export async function publishRealtimeChannelBatch(
  batchId: string,
  messages: ReadonlyArray<{ data: Uint8Array; binary?: boolean; metadata?: Record<string,string> }>,
  submit: (request: RealtimeBatchRequest) => Promise<unknown>,
  options: { expectedSequence?: number } = {},
): Promise<RealtimeBatchResult> {
  if (!batchId || batchId.trim() !== batchId || /[\0\r\n]/.test(batchId) || new TextEncoder().encode(batchId).byteLength > 128 || messages.length < 1 || messages.length > 32) {
    throw new RealtimeConfigurationError('invalid batch ID or message count');
  }
  let total = 0;
  const encoded = messages.map(message => {
    if (!(message.data instanceof Uint8Array) || message.data.length > 4096 || (message.binary !== undefined && typeof message.binary !== 'boolean')) throw new RealtimeConfigurationError('invalid batch message');
    validateRealtimeMetadata(message.metadata);
    total += message.data.length;
    return { metadata: message.metadata, data_base64: btoa(String.fromCharCode(...message.data)), binary: message.binary ?? false };
  });
  if (total > 65536) throw new RealtimeConfigurationError('batch exceeds 64 KiB');
  const precondition = options.expectedSequence === undefined ? {} : realtimeExpectedSequence(options.expectedSequence);
  const result = await submit({ ...precondition, batch_id: batchId, messages: encoded });
  if (!objectValue(result) || result.batch_id !== batchId || result.durable !== true || typeof result.partial !== 'boolean' || !Array.isArray(result.messages) || result.messages.length !== messages.length) throw new RealtimeProtocolError('invalid batch response');
  let previous = 0;
  for (const message of result.messages) {
    if (!objectValue(message) || message.durable !== true || typeof message.partial !== 'boolean') throw new RealtimeProtocolError('invalid batch outcome');
    const current = sequence(message.sequence, 'batch sequence');
    if (current < 1 || (previous > 0 && current !== previous + 1)) throw new RealtimeProtocolError('batch sequence gap');
    previous = current;
  }
  return result as unknown as RealtimeBatchResult;
}

function validateRealtimeMetadata(value: unknown): void {
 if (value === undefined || value === null) return;
 if (!objectValue(value) || new TextEncoder().encode(JSON.stringify(value)).byteLength>4096 || Object.keys(value).length>8 || Object.entries(value).some(([key,item]) => !/^[a-z0-9_.-]{1,64}$/.test(key) || typeof item!=='string' || new TextEncoder().encode(item).byteLength>128 || /[\0\r\n]/.test(item))) throw new RealtimeProtocolError('invalid realtime metadata or filter');
}

/** Select an immutable registered schema; schema fields count toward the eight metadata entries. */
export function realtimeEventSchemaMetadata(eventType: string, version: number, metadata: Record<string,string> = {}): Record<string,string> {
 if (!/^[a-z0-9_.-]{1,64}$/.test(eventType) || !Number.isSafeInteger(version) || version<1 || version>1000000) throw new RealtimeConfigurationError('invalid event schema identity');
 const result = { ...metadata, event_type: eventType, schema_version: String(version) };
 validateRealtimeMetadata(result);
 return result;
}

function realtimeReducerDeadlineValid(value: unknown): value is string {
  return typeof value === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(value) && Number.isFinite(Date.parse(value));
}
export type RealtimeReducerCondition =
  | { field: string; equals: unknown; absent?: never }
  | { field: string; absent: true; equals?: never };
export type RealtimeReducerOperation = { conditions?: RealtimeReducerCondition[] } & (
  | { op: 'set' | 'merge'; key: string; value: Record<string, unknown>; expected_version?: number; expires_at?: string | null }
  | { op: 'increment'; key: string; field: string; delta: number; min?: number; max?: number; expected_version?: number; expires_at?: string | null }
  | { op: 'append'; key: string; field: string; items: unknown[]; unique?: boolean; max_length?: number; expected_version?: number; expires_at?: string | null }
  | { op: 'remove'; key: string; field: string; items: unknown[]; max_length?: number; expected_version?: number; expires_at?: string | null }
  | { op: 'delete'; key: string; expected_version?: number });
/** Encode a built-in reducer operation for retained publishing or a batch item. */
export function realtimeReducerEvent(operation: RealtimeReducerOperation): Uint8Array {
  if (!operation || typeof operation.key!=='string' || !operation.key || operation.key.trim()!==operation.key || new TextEncoder().encode(operation.key).byteLength>128 || /[\0\r\n]/.test(operation.key) || !['set','merge','increment','append','remove','delete'].includes(operation.op)) throw new RealtimeConfigurationError('invalid reducer operation');
  if (operation.expected_version !== undefined && (!Number.isSafeInteger(operation.expected_version) || operation.expected_version < 0)) throw new RealtimeConfigurationError('expected version must be a nonnegative safe integer');
  if (operation.conditions !== undefined) {
    if (!Array.isArray(operation.conditions) || operation.conditions.length < 1 || operation.conditions.length > 16) throw new RealtimeConfigurationError('conditions must contain 1..16 predicates');
    for (const condition of operation.conditions) {
      if (!objectValue(condition) || typeof condition.field !== 'string' || !condition.field || condition.field.trim() !== condition.field || new TextEncoder().encode(condition.field).byteLength > 128 || /[\0\r\n]/.test(condition.field) || Object.keys(condition).some(key => !['field','equals','absent'].includes(key))) throw new RealtimeConfigurationError('invalid condition field or properties');
      const hasEquals = Object.prototype.hasOwnProperty.call(condition, 'equals');
      const hasAbsent = Object.prototype.hasOwnProperty.call(condition, 'absent');
      if (hasEquals === hasAbsent || (hasAbsent && condition.absent !== true)) throw new RealtimeConfigurationError('condition requires exactly one of equals or absent:true');
      if (hasEquals) {
        try {
          JSON.stringify(condition.equals, (_key, value: unknown) => {
            if (value === undefined || typeof value === 'function' || typeof value === 'symbol' || typeof value === 'bigint' || (typeof value === 'number' && !Number.isFinite(value))) throw new Error('invalid JSON condition');
            return value;
          });
        } catch { throw new RealtimeConfigurationError('condition equals must be a JSON value'); }
      }
    }
  }
  const deadline = (operation as { expires_at?: unknown }).expires_at;
  if (deadline !== undefined &&
      (operation.op === 'delete' || (deadline !== null && !realtimeReducerDeadlineValid(deadline)))) {
    throw new RealtimeConfigurationError('expires_at must be an RFC3339 timestamp or null on set/merge/increment/append/remove');
  }
  if ((operation.op==='set' || operation.op==='merge') && !objectValue(operation.value)) throw new RealtimeConfigurationError('reducer value must be an object');
  if (operation.op === 'increment') {
    if (typeof operation.field !== 'string' || !operation.field || operation.field.trim() !== operation.field || new TextEncoder().encode(operation.field).byteLength > 128 || /[\0\r\n]/.test(operation.field)) throw new RealtimeConfigurationError('invalid counter field');
    if (!Number.isSafeInteger(operation.delta) || (operation.min !== undefined && !Number.isSafeInteger(operation.min)) || (operation.max !== undefined && !Number.isSafeInteger(operation.max))) throw new RealtimeConfigurationError('counter delta and bounds must be safe integers');
    if (operation.min !== undefined && operation.max !== undefined && operation.min > operation.max) throw new RealtimeConfigurationError('counter min must not exceed max');
  }
  if (operation.op === 'append' || operation.op === 'remove') {
    if (typeof operation.field !== 'string' || !operation.field || operation.field.trim() !== operation.field || new TextEncoder().encode(operation.field).byteLength > 128 || /[\0\r\n]/.test(operation.field)) throw new RealtimeConfigurationError('invalid array field');
    if (!Array.isArray(operation.items) || operation.items.length < 1 || operation.items.length > 32) throw new RealtimeConfigurationError('array items must contain 1..32 values');
    try {
      JSON.stringify(operation.items, (_key, value: unknown) => {
        if (value === undefined || typeof value === 'function' || typeof value === 'symbol' || typeof value === 'bigint' || (typeof value === 'number' && !Number.isFinite(value))) throw new Error('invalid JSON item');
        return value;
      });
    } catch { throw new RealtimeConfigurationError('array items must be JSON serializable values'); }
    if (operation.max_length !== undefined && (!Number.isInteger(operation.max_length) || operation.max_length < 0 || operation.max_length > 256)) throw new RealtimeConfigurationError('max_length must be an integer from 0 to 256');
    const unique = (operation as { unique?: unknown }).unique;
    if (unique !== undefined && (operation.op !== 'append' || typeof unique !== 'boolean')) throw new RealtimeConfigurationError('unique must be a boolean on append');
  }
  let encoded: Uint8Array;
  try { encoded = new TextEncoder().encode(JSON.stringify(operation)); } catch { throw new RealtimeConfigurationError('reducer operation must be JSON serializable'); }
  if (encoded.byteLength>4096) throw new RealtimeConfigurationError('reducer event exceeds 4 KiB');
  return encoded;
}

/** Zero means an empty channel; omit the field for an unconditional publish. */
export function realtimeExpectedSequence(expected: number): { expected_sequence: number } {
  if (!Number.isSafeInteger(expected) || expected<0) throw new RealtimeConfigurationError('expected sequence must be a nonnegative safe integer');
  return { expected_sequence: expected };
}


export type RealtimeScheduleCondition =
  | { key: string; exists: boolean; field?: never }
  | { key: string; field: string; equals: unknown }
  | { key: string; field: string; lt: number }
  | { key: string; field: string; lte: number }
  | { key: string; field: string; gt: number }
  | { key: string; field: string; gte: number };
export interface RealtimeScheduleRequest {
  group?: string;
  conditions?: RealtimeScheduleCondition[];
  on_condition_failure?: 'skip' | 'retry';
  interval_seconds?: number;
  max_occurrences?: number;
  end_at?: string;
  max_attempts?: number;
  backoff_seconds?: number;
  data_base64: string;
  binary: boolean;
  metadata?: Record<string, string>;
  deliver_at: string;
}
export interface RealtimeSchedule extends RealtimeScheduleRequest {
  skipped_occurrences: number;
  skip_reason?: string;
  initial_deliver_at: string;
  occurrence: number;
  completed_occurrences: number;
  max_attempts: number;
  backoff_seconds: number;
  attempts: number;
  cycle_attempts: number;
  next_attempt_at?: string;
  last_attempt_at?: string;
  schedule_id: string;
  channel: string;
  version: number;
  status: 'pending' | 'paused' | 'published' | 'canceled' | 'failed' | 'skipped';
  sequence?: number;
  last_error?: string;
  created_at: string;
  updated_at: string;
}
/** Submit to a channel schedule PUT through an authorized backend; reuse its ID when retrying. */
export function realtimeScheduledEvent(
  data: Uint8Array,
  deliverAt: string,
  options: { group?: string; binary?: boolean; metadata?: Record<string, string>; maxAttempts?: number; backoffSeconds?: number; intervalSeconds?: number; maxOccurrences?: number; endAt?: string; conditions?: RealtimeScheduleCondition[]; onConditionFailure?: 'skip' | 'retry' } = {},
): RealtimeScheduleRequest {
  if (options.group !== undefined && !realtimeScheduleGroupValid(options.group)) throw new RealtimeConfigurationError('group must be 1..128 UTF-8 bytes without surrounding whitespace or control delimiters');
  if (!(data instanceof Uint8Array) || data.byteLength > 4096) throw new RealtimeConfigurationError('scheduled event must be at most 4 KiB');
  if (!realtimeReducerDeadlineValid(deliverAt)) throw new RealtimeConfigurationError('deliver_at must be an RFC3339 timestamp');
  if (options.binary !== undefined && typeof options.binary !== 'boolean') throw new RealtimeConfigurationError('binary must be a boolean');
  if (options.maxAttempts !== undefined && (!Number.isInteger(options.maxAttempts) || options.maxAttempts < 1 || options.maxAttempts > 10)) throw new RealtimeConfigurationError('maxAttempts must be an integer from 1 to 10');
  if (options.backoffSeconds !== undefined && (!Number.isInteger(options.backoffSeconds) || options.backoffSeconds < 5 || options.backoffSeconds > 3600)) throw new RealtimeConfigurationError('backoffSeconds must be an integer from 5 to 3600');
  if (options.intervalSeconds !== undefined && (!Number.isInteger(options.intervalSeconds) || options.intervalSeconds < 5 || options.intervalSeconds > 2592000)) throw new RealtimeConfigurationError('intervalSeconds must be an integer from 5 to 2592000');
  if (options.maxOccurrences !== undefined && (!Number.isInteger(options.maxOccurrences) || options.maxOccurrences < 0 || options.maxOccurrences > 1000000)) throw new RealtimeConfigurationError('maxOccurrences must be an integer from 0 to 1000000');
  if ((options.maxOccurrences !== undefined || options.endAt !== undefined) && options.intervalSeconds === undefined) throw new RealtimeConfigurationError('recurrence limits require intervalSeconds');
  if (options.endAt !== undefined && (!realtimeReducerDeadlineValid(options.endAt) || Date.parse(options.endAt) < Date.parse(deliverAt) || Date.parse(options.endAt) > Date.parse(deliverAt) + 365 * 86400000)) throw new RealtimeConfigurationError('endAt must be between deliverAt and one year later');
  if (options.intervalSeconds !== undefined && options.metadata && (Object.keys(options.metadata).length > 6 || Object.prototype.hasOwnProperty.call(options.metadata, 'schedule_id') || Object.prototype.hasOwnProperty.call(options.metadata, 'schedule_occurrence'))) throw new RealtimeConfigurationError('recurring schedules reserve two metadata fields for occurrence identity');
  if (options.onConditionFailure !== undefined && (options.onConditionFailure !== 'skip' && options.onConditionFailure !== 'retry' || options.conditions === undefined)) throw new RealtimeConfigurationError('onConditionFailure requires conditions and must be skip or retry');
  if (options.conditions !== undefined) {
    if (!Array.isArray(options.conditions) || options.conditions.length < 1 || options.conditions.length > 16) throw new RealtimeConfigurationError('scheduled conditions require 1..16 predicates');
    for (const condition of options.conditions) {
      if (!objectValue(condition) || typeof condition.key !== 'string' || !condition.key || condition.key.trim() !== condition.key || new TextEncoder().encode(condition.key).byteLength > 128 || /[\0\r\n]/.test(condition.key)) throw new RealtimeConfigurationError('invalid scheduled condition key');
      const record = condition as unknown as Record<string, unknown>;
      const operators = ['exists','equals','lt','lte','gt','gte'].filter(operator => Object.prototype.hasOwnProperty.call(record, operator));
      if (operators.length !== 1 || Object.keys(record).some(key => !['key','field','exists','equals','lt','lte','gt','gte'].includes(key))) throw new RealtimeConfigurationError('scheduled condition requires exactly one operator');
      const operator = operators[0];
      if (operator === 'exists') {
        if (typeof record.exists !== 'boolean' || Object.prototype.hasOwnProperty.call(record, 'field')) throw new RealtimeConfigurationError('exists checks entities and cannot include field');
      } else {
        if (typeof record.field !== 'string' || !record.field || record.field.trim() !== record.field || new TextEncoder().encode(record.field).byteLength > 128 || /[\0\r\n]/.test(record.field)) throw new RealtimeConfigurationError('invalid scheduled condition field');
        if (operator !== 'equals' && !Number.isSafeInteger(record[operator])) throw new RealtimeConfigurationError('scheduled thresholds must be safe integers');
      }
    }
    try {
      const encoded = JSON.stringify(options.conditions, (_key, value: unknown) => {
        if (value === undefined || typeof value === 'function' || typeof value === 'symbol' || typeof value === 'bigint' || (typeof value === 'number' && !Number.isFinite(value))) throw new Error('invalid JSON condition');
        return value;
      });
      if (new TextEncoder().encode(encoded).byteLength > 4096) throw new Error('conditions too large');
    } catch { throw new RealtimeConfigurationError('scheduled conditions must be JSON values within 4 KiB'); }
  }
  validateRealtimeMetadata(options.metadata);
  return { group: options.group, data_base64: btoa(String.fromCharCode(...data)), binary: options.binary ?? false, metadata: options.metadata, deliver_at: deliverAt, max_attempts: options.maxAttempts, backoff_seconds: options.backoffSeconds, interval_seconds: options.intervalSeconds, max_occurrences: options.maxOccurrences, end_at: options.endAt, conditions: options.conditions, on_condition_failure: options.onConditionFailure };
}

export interface RealtimeScheduleRetryRequest {
  expected_version: number;
  deliver_at?: string;
}
/** Submit to the schedule retry POST via your authorized backend. */
export function realtimeScheduleRetry(expectedVersion: number, deliverAt?: string): RealtimeScheduleRetryRequest {
  if (!Number.isSafeInteger(expectedVersion) || expectedVersion < 1) throw new RealtimeConfigurationError('expected version must be a positive safe integer');
  if (deliverAt !== undefined && !realtimeReducerDeadlineValid(deliverAt)) throw new RealtimeConfigurationError('deliver_at must be an RFC3339 timestamp');
  return { expected_version: expectedVersion, deliver_at: deliverAt };
}


export interface RealtimeScheduleHistoryEvent {
  skipped_occurrences: number;
  skip_reason?: string;
  occurrence: number;
  completed_occurrences: number;
  version: number;
  event: 'baseline' | 'created' | 'rescheduled' | 'canceled' | 'manual_retry' | 'attempt_failed' | 'published' | 'paused' | 'resumed' | 'skipped';
  status: RealtimeSchedule['status'];
  attempts: number;
  cycle_attempts: number;
  deliver_at: string;
  next_attempt_at?: string;
  failure_code?: string;
  sequence?: number;
  occurred_at: string;
}
export interface RealtimeScheduleHistory {
  schedule_id: string;
  channel: string;
  oldest_version: number;
  latest_version: number;
  history_truncated: boolean;
  has_more: boolean;
  events: RealtimeScheduleHistoryEvent[];
}

/** Payload for realtime.schedule.published, .failed, and .skipped app webhooks. */
export interface RealtimeScheduleCompletionWebhookPayload {
  event_id: string;
  app_id: string;
  endpoint_id: string;
  channel: string;
  schedule_id: string;
  version: number;
  occurrence: number;
  completed_occurrences: number;
  skipped_occurrences: number;
  outcome: 'published' | 'failed' | 'skipped';
  attempts: number;
  cycle_attempts: number;
  deliver_at: string;
  occurred_at: string;
  sequence?: number;
  failure_code?: string;
  skip_reason?: string;
}

export interface RealtimeScheduleTotals {
  pending: number; paused: number; published: number; failed: number;
  skipped: number; canceled: number;
  completed_occurrences: number; skipped_occurrences: number;
}
export interface RealtimeScheduleList {
  schedules: RealtimeSchedule[];
  totals: RealtimeScheduleTotals;
}
export interface RealtimeScheduleGroupRequest { expected_versions: Record<string, number>; }
function realtimeScheduleGroupValid(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.trim() === value && new TextEncoder().encode(value).byteLength <= 128 && !/[\0\r\n]/.test(value);
}
/** Build a bulk action body from every pending/paused group member in a fresh list. */
export function realtimeScheduleGroupRequest(group: string, schedules: RealtimeSchedule[]): RealtimeScheduleGroupRequest {
  if (!realtimeScheduleGroupValid(group) || !Array.isArray(schedules)) throw new RealtimeConfigurationError('invalid schedule group');
  const expected_versions: Record<string, number> = Object.create(null) as Record<string, number>;
  for (const schedule of schedules) {
    if (schedule.group !== group || (schedule.status !== 'pending' && schedule.status !== 'paused')) continue;
    if (!realtimeScheduleGroupValid(schedule.schedule_id) || !Number.isSafeInteger(schedule.version) || schedule.version < 1 || Object.prototype.hasOwnProperty.call(expected_versions, schedule.schedule_id)) throw new RealtimeConfigurationError('invalid or duplicate schedule identity/version');
    expected_versions[schedule.schedule_id] = schedule.version;
  }
  if (Object.keys(expected_versions).length > 256) throw new RealtimeConfigurationError('group exceeds schedule capacity');
  return { expected_versions };
}

export interface RealtimeBackendSignalRequest { data: unknown; name?: string; ttl_ms?: number; }
export interface RealtimeBackendSignalResponse { accepted: boolean; member_id: string; expires_at?: string; }
/** Submit through an authorized backend to the channel signals POST API. */
export function realtimeBackendSignal(data: unknown, options: { name?: string; ttlMs?: number } = {}): RealtimeBackendSignalRequest {
  if (options.name !== undefined && !/^[A-Za-z0-9_.:-]{1,64}$/.test(options.name)) throw new RealtimeConfigurationError('invalid signal name');
  if (options.ttlMs !== undefined && (options.name === undefined || !Number.isInteger(options.ttlMs) || options.ttlMs < 0 || options.ttlMs > 30000)) throw new RealtimeConfigurationError('ttlMs requires a name and must be 0..30000');
  const encoded = encodeSignalData(data);
  return { data: JSON.parse(encoded) as unknown, name: options.name, ttl_ms: options.ttlMs };
}

export interface RealtimeSignalCoalescer {
  /** Send immediately when idle; otherwise retain only the latest JSON snapshot. */
  send(data: unknown): void;
  /** Send the pending final value immediately; throws if the channel is unavailable. */
  flush(): void;
  /** Discard the pending value without sending a clear frame. */
  cancel(): void;
  /** Flush the final value and stop; use false to discard instead. */
  dispose(flush?: boolean): void;
}
export interface RealtimeSignalCoalescerOptions {
  intervalMs?: number;
  ttlMs?: number;
  /** Timer-send errors leave the latest value pending for an explicit flush/retry. */
  onError(error: unknown): void;
}
/** Throttle named live updates with a leading send and a trailing latest-value send. */
export function createRealtimeSignalCoalescer(
  actions: Pick<RealtimeChannelActions, 'sendTemporarySignal'>,
  name: string,
  options: RealtimeSignalCoalescerOptions,
): RealtimeSignalCoalescer {
  const intervalMs = options.intervalMs ?? 100;
  const ttlMs = options.ttlMs ?? 5000;
  if (!actions || typeof actions.sendTemporarySignal !== 'function' || !/^[A-Za-z0-9_.:-]{1,64}$/.test(name) ||
      !Number.isInteger(intervalMs) || intervalMs < 50 || intervalMs > 30000 ||
      !Number.isInteger(ttlMs) || ttlMs < 1 || ttlMs > 30000 || typeof options.onError !== 'function') {
    throw new RealtimeConfigurationError('coalescer requires a named sender, intervalMs 50..30000, ttlMs 1..30000, and onError');
  }
  let pending: string | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let lastSent: number | undefined;
  let disposed = false;
  const stopTimer = (): void => { if (timer !== undefined) clearTimeout(timer); timer = undefined; };
  const flush = (): void => {
    stopTimer();
    if (pending === undefined) return;
    const encoded = pending;
    actions.sendTemporarySignal(name, JSON.parse(encoded) as unknown, ttlMs);
    if (pending === encoded) pending = undefined;
    lastSent = Date.now();
  };
  return {
    send(data) {
      if (disposed) throw new RealtimeConfigurationError('signal coalescer is disposed');
      pending = encodeSignalData(data);
      const wait = lastSent === undefined ? 0 : Math.max(0, intervalMs - (Date.now() - lastSent));
      if (wait === 0) { flush(); return; }
      if (timer === undefined) timer = setTimeout(() => {
        timer = undefined;
        try { flush(); } catch (error) { options.onError(error); }
      }, wait);
    },
    flush,
    cancel() { stopTimer(); pending = undefined; },
    dispose(sendFinal = true) {
      if (disposed) return;
      try { if (sendFinal) flush(); }
      finally { stopTimer(); pending = undefined; disposed = true; }
    },
  };
}

/** Canonical channel for backend-authorized, isolated activity scope subscriptions. */
export function realtimeActivityScopeChannel(parentChannel: string, scope: string): string {
  validateChannel(parentChannel);
  if (parentChannel.startsWith('__activity.') || /\0/.test(parentChannel) || typeof scope !== 'string' || !scope || scope.trim() !== scope || /[\0/?#\r\n]/.test(scope) || new TextEncoder().encode(scope).byteLength > 64) throw new RealtimeConfigurationError('invalid activity parent or scope');
  const encode = (value: string): string => btoa(String.fromCharCode(...new TextEncoder().encode(value))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  const channel = `__activity.${encode(parentChannel)}.${encode(scope)}`;
  validateChannel(channel);
  return channel;
}
