// A realtime client for apps whose views come and go: one WebSocket per
// endpoint, channels subscribed while something watches them, and an
// immutable view per channel (status, recent messages, presence, occupancy)
// that UI frameworks can render. React hooks live in `@gregale/sdk-node/react`.

import { createRealtimePresenceDirectory, type RealtimePresenceDirectory } from './realtime-activity-summary.js';
import {
  consumeRealtimeChannels,
  REALTIME_MAX_CHANNELS_PER_CONNECTION,
  type RealtimeChannelActions,
  type RealtimeChannelController,
  type RealtimeConnectionOptions,
  type RealtimeMessage,
  type RealtimePresenceMember,
} from './realtime-resume.js';

export interface RealtimeClientOptions {
  /** Full wss:// endpoint URL, including the endpoint ID. */
  url: string;
  /**
   * Opens the WebSocket with credentials, for example
   * `createBrowserRealtimeTicketSocketFactory(fetchTicket)`; called again on
   * every reconnect.
   */
  webSocketFactory: RealtimeConnectionOptions['webSocketFactory'];
  retryInitialMs?: number;
  retryMaxMs?: number;
  connectTimeoutMs?: number;
  /** Messages kept in each channel view, oldest dropped first (default 50, 1-1000). */
  messageLimit?: number;
  /**
   * Keep a channel subscribed this long after its last watcher leaves
   * (default 1000 ms), so re-renders and quick navigation do not resubscribe.
   */
  lingerMs?: number;
  /** Receives errors the connection cannot recover from. */
  onError?(error: unknown): void;
}

export type RealtimeChannelStatus = 'idle' | 'connecting' | 'live' | 'reconnecting' | 'error' | 'closed';

export interface RealtimeChannelView {
  readonly channel: string;
  readonly status: RealtimeChannelStatus;
  /** The most recent messages, oldest first, up to `messageLimit`. */
  readonly messages: readonly RealtimeMessage[];
  /** Members present on the channel, sorted by member ID. */
  readonly presence: readonly RealtimePresenceMember[];
  /** Subscribers across all realtime nodes, when watched with `occupancy: true`. */
  readonly occupancy?: number;
  /** Why the status is `error`: a server code such as `not_authorized`, or a message. */
  readonly error?: string;
}

export interface RealtimeChannelWatchOptions {
  /** This client's presence state on the channel (up to 512 bytes of JSON). */
  presence?: Record<string, unknown>;
  /** Group presence by the verified principal instead of by connection. */
  presenceScope?: 'connection' | 'principal';
  /** Keep `view.occupancy` current with the channel's subscriber count. */
  occupancy?: boolean;
  /** Exact-match metadata filter. */
  filter?: Record<string, string>;
  /** Side effects for each message, in order; the view updates either way. */
  onMessage?(message: RealtimeMessage): void;
}

export interface RealtimeChannelHandle {
  readonly channel: string;
  /** The current view. The same object is returned until something changes. */
  getView(): RealtimeChannelView;
  /**
   * Listen for view changes. The first listener subscribes the channel; it is
   * unsubscribed `lingerMs` after the last one leaves. Returns the remover.
   */
  subscribe(listener: () => void): () => void;
  /** Change this client's presence state; applied on the next subscribe when not live. */
  updatePresence(state: Record<string, unknown>): void;
  /** Send a transient signal to the channel's other subscribers; requires a live channel. */
  sendSignal(data: unknown): void;
}

export interface RealtimeClient {
  /**
   * The handle for a channel. Options apply when the channel is first
   * watched and later calls return the same handle, except that a later
   * `occupancy: true` turns occupancy on while the channel is not yet watched.
   */
  channel(name: string, options?: RealtimeChannelWatchOptions): RealtimeChannelHandle;
  /** Close the connection and stop every channel. */
  close(): void;
}

interface HandleState {
  readonly name: string;
  options: RealtimeChannelWatchOptions;
  readonly listeners: Set<() => void>;
  view: RealtimeChannelView;
  messages: RealtimeMessage[];
  cursor: number;
  presence?: Record<string, unknown>;
  active: boolean;
  actions?: RealtimeChannelActions;
  linger?: ReturnType<typeof setTimeout>;
  handle: RealtimeChannelHandle;
}

/** Create a realtime client for one endpoint. */
export function createRealtimeClient(options: RealtimeClientOptions): RealtimeClient {
  const messageLimit = options.messageLimit ?? 50;
  const lingerMs = options.lingerMs ?? 1000;
  if (!Number.isInteger(messageLimit) || messageLimit < 1 || messageLimit > 1000) throw new TypeError('messageLimit must be an integer from 1 to 1000');
  if (!Number.isFinite(lingerMs) || lingerMs < 0) throw new TypeError('lingerMs must be zero or more');
  const handles = new Map<string, HandleState>();
  let controller: RealtimeChannelController | undefined;
  let connection: AbortController | undefined;
  let closed = false;
  const presence: RealtimePresenceDirectory = createRealtimePresenceDirectory({
    maxMembers: 8192,
    onChange(channel, members) {
      const state = handles.get(channel);
      if (state) update(state, { presence: members });
    },
  });

  function update(state: HandleState, change: Partial<RealtimeChannelView>): void {
    state.view = Object.freeze({ ...state.view, ...change });
    for (const listener of [...state.listeners]) listener();
  }

  function activeCount(): number {
    let count = 0;
    for (const state of handles.values()) if (state.active) count++;
    return count;
  }

  // Undefined when the consumer rejected its options; its rejection then
  // marks the active channels as errored.
  function connect(): RealtimeChannelController | undefined {
    if (controller) return controller;
    const abort = new AbortController();
    connection = abort;
    let created: RealtimeChannelController | undefined;
    consumeRealtimeChannels({
      url: options.url, webSocketFactory: options.webSocketFactory, signal: abort.signal, channels: [],
      retryInitialMs: options.retryInitialMs, retryMaxMs: options.retryMaxMs, connectTimeoutMs: options.connectTimeoutMs,
      controller: (value) => { created = value; },
      onConnectionChange(change) {
        for (const state of handles.values()) {
          if (!state.active) continue;
          if (change === 'disconnected') {
            state.actions = undefined;
            update(state, { status: 'reconnecting' });
          }
        }
      },
    }).catch((error: unknown) => {
      if (abort.signal.aborted) return;
      controller = undefined;
      connection = undefined;
      const message = error instanceof Error ? error.message : String(error);
      for (const state of handles.values()) {
        if (!state.active) continue;
        state.active = false;
        state.actions = undefined;
        update(state, { status: 'error', error: message });
      }
      options.onError?.(error);
    });
    controller = created;
    return controller;
  }

  function disconnectIfIdle(): void {
    if (activeCount() > 0 || !connection) return;
    connection.abort();
    connection = undefined;
    controller = undefined;
  }

  function activate(state: HandleState): void {
    if (state.active || closed) return;
    if (activeCount() >= REALTIME_MAX_CHANNELS_PER_CONNECTION) {
      update(state, { status: 'error', error: `a realtime client watches at most ${REALTIME_MAX_CHANNELS_PER_CONNECTION} channels at once` });
      return;
    }
    state.active = true;
    update(state, { status: 'connecting', error: undefined });
    const { options: watch } = state;
    const channels = connect();
    if (!channels) return;
    channels.add({
      channel: state.name,
      filter: watch.filter,
      presence: state.presence,
      presenceScope: watch.presenceScope,
      cursorStore: { load: () => state.cursor, save: (next) => { state.cursor = next; } },
      onMessage(message) {
        state.messages = [...state.messages, message].slice(-messageLimit);
        update(state, { messages: state.messages });
        watch.onMessage?.(message);
      },
      onSubscribed(actions) {
        state.actions = actions;
        update(state, { status: 'live' });
      },
      onPresence(event) { presence.apply(event); },
      onOccupancy: watch.occupancy ? ({ subscribers }) => update(state, { occupancy: subscribers }) : undefined,
      onResync(error) {
        // Retained history moved past this view; start again from the oldest kept message.
        state.messages = [];
        update(state, { messages: state.messages });
        return error.oldestSequence - 1;
      },
      onSubscriptionError(code) {
        state.active = false;
        state.actions = undefined;
        update(state, { status: 'error', error: code });
        disconnectIfIdle();
      },
    }).catch((error: unknown) => {
      state.active = false;
      update(state, { status: 'error', error: error instanceof Error ? error.message : String(error) });
      disconnectIfIdle();
    });
  }

  function deactivate(state: HandleState): void {
    if (!state.active) return;
    state.active = false;
    state.actions = undefined;
    controller?.remove(state.name);
    presence.reset(state.name);
    update(state, { status: 'idle', occupancy: undefined });
    disconnectIfIdle();
  }

  function createHandle(name: string, watch: RealtimeChannelWatchOptions): HandleState {
    const state: HandleState = {
      name, options: watch, listeners: new Set(), messages: [], cursor: 0, presence: watch.presence, active: false,
      view: Object.freeze({ channel: name, status: 'idle', messages: [], presence: [] }),
      handle: undefined as unknown as RealtimeChannelHandle,
    };
    state.handle = {
      channel: name,
      getView: () => state.view,
      subscribe(listener) {
        if (closed) return () => {};
        state.listeners.add(listener);
        if (state.linger) { clearTimeout(state.linger); state.linger = undefined; }
        activate(state);
        return () => {
          if (!state.listeners.delete(listener) || state.listeners.size > 0) return;
          state.linger = setTimeout(() => {
            state.linger = undefined;
            if (state.listeners.size === 0) deactivate(state);
          }, lingerMs);
        };
      },
      updatePresence(next) {
        state.presence = next;
        state.actions?.updatePresence(next);
      },
      sendSignal(data) {
        if (!state.actions) throw new Error(`realtime channel ${name} is not live`);
        state.actions.sendSignal(data);
      },
    };
    return state;
  }

  return {
    channel(name, watch = {}) {
      if (closed) throw new Error('realtime client is closed');
      let state = handles.get(name);
      if (!state) {
        state = createHandle(name, watch);
        handles.set(name, state);
      } else if (watch.occupancy && !state.options.occupancy && !state.active) {
        state.options = { ...state.options, occupancy: true };
      }
      return state.handle;
    },
    close() {
      if (closed) return;
      closed = true;
      connection?.abort();
      connection = undefined;
      controller = undefined;
      for (const state of handles.values()) {
        if (state.linger) clearTimeout(state.linger);
        state.active = false;
        state.actions = undefined;
        update(state, { status: 'closed' });
      }
    },
  };
}
