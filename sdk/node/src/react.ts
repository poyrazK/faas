// React bindings for the realtime client (`@gregale/sdk-node/react`).
//
//   const client = createRealtimeClient({ url, webSocketFactory });
//   <RealtimeProvider client={client}><Room /></RealtimeProvider>
//
//   function Room() {
//     const { status, messages, presence } = useRealtimeChannel('room-1', { presence: { name } });
//     const watching = useOccupancy('room-1');
//   }
//
// A channel is subscribed while a component watches it and released shortly
// after the last one unmounts. React 18 or later is required.

import { createContext, createElement, useContext, useMemo, useSyncExternalStore, type ReactNode } from 'react';

import type {
  RealtimeChannelView,
  RealtimeChannelWatchOptions,
  RealtimeClient,
} from './realtime-client.js';
import type { RealtimePresenceMember } from './realtime-resume.js';

const RealtimeClientContext = createContext<RealtimeClient | null>(null);

export interface RealtimeProviderProps {
  client: RealtimeClient;
  children?: ReactNode;
}

/** Makes a realtime client available to the hooks below it. */
export function RealtimeProvider({ client, children }: RealtimeProviderProps): ReturnType<typeof createElement> {
  return createElement(RealtimeClientContext.Provider, { value: client }, children);
}

/** The client from the nearest RealtimeProvider. */
export function useRealtimeClient(): RealtimeClient {
  const client = useContext(RealtimeClientContext);
  if (!client) throw new Error('useRealtimeClient needs a <RealtimeProvider client={...}> above it');
  return client;
}

export interface UseRealtimeChannelOptions extends RealtimeChannelWatchOptions {
  /** Use this client instead of the provider's. */
  client?: RealtimeClient;
}

export interface UseRealtimeChannelResult extends RealtimeChannelView {
  /** Change this client's presence state on the channel. */
  updatePresence(state: Record<string, unknown>): void;
  /** Send a transient signal; throws while the channel is not live. */
  sendSignal(data: unknown): void;
}

/**
 * Watch a channel: re-renders with its status, recent messages, presence and
 * occupancy. Options apply when the channel is first watched.
 */
export function useRealtimeChannel(channel: string, options: UseRealtimeChannelOptions = {}): UseRealtimeChannelResult {
  const contextClient = useContext(RealtimeClientContext);
  const client = options.client ?? contextClient;
  if (!client) throw new Error('useRealtimeChannel needs a <RealtimeProvider> or a client option');
  const { client: _client, ...watch } = options;
  // Options apply on first watch, so they are not dependencies.
  const handle = useMemo(() => client.channel(channel, watch), [client, channel]);
  const view = useSyncExternalStore(handle.subscribe, handle.getView, handle.getView);
  return useMemo(() => ({ ...view, updatePresence: handle.updatePresence, sendSignal: handle.sendSignal }), [view, handle]);
}

/** The members present on a channel. */
export function usePresence(channel: string, options: UseRealtimeChannelOptions = {}): readonly RealtimePresenceMember[] {
  return useRealtimeChannel(channel, options).presence;
}

/** The channel's subscriber count across all realtime nodes; undefined until known. */
export function useOccupancy(channel: string, options: UseRealtimeChannelOptions = {}): number | undefined {
  return useRealtimeChannel(channel, { ...options, occupancy: true }).occupancy;
}
