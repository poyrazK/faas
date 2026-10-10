import { RealtimeConfigurationError, type RealtimePresenceEvent, type RealtimePresenceMember } from './realtime-resume.js';
import { type RealtimeActivity } from './realtime-activity.js';

export interface RealtimeActivityParticipant {
  memberId: string;
  label: string;
  state: Record<string, unknown>;
  connectionCount: number;
  activities: RealtimeActivity[];
}
export interface RealtimeActivitySummary {
  channel: string;
  /** All presence members, regardless of named activity. */
  viewers: RealtimeActivityParticipant[];
  /** Members with at least one matching, unexpired signal. */
  active: RealtimeActivityParticipant[];
  viewerCount: number;
  connectionCount: number;
}
export interface RealtimeActivitySummaryOptions {
  channel: string;
  names?: string[];
  excludeMemberId?: string;
  /** Labels are presentation data, never verified identity. Defaults to memberId. */
  label?(member: RealtimePresenceMember): string;
  /** Include senders without presence, such as backend signals; default false. */
  includeUnknownMembers?: boolean;
}
function jsonCopy<T>(value: T): T { return JSON.parse(JSON.stringify(value)) as T; }
function participant(member: RealtimePresenceMember, label?: (member: RealtimePresenceMember) => string): RealtimeActivityParticipant {
  const copy = jsonCopy(member);
  const text = label?.(jsonCopy(copy)) ?? copy.memberId;
  if (typeof text !== 'string' || text.length > 512) throw new RealtimeConfigurationError('activity labels must be strings within 512 characters');
  return { ...copy, label: text, activities: [] };
}
/** Pure UI projection; feed current tracker activity and the channel's presence snapshot. */
export function aggregateRealtimeActivity(
  activities: RealtimeActivity[], members: RealtimePresenceMember[], options: RealtimeActivitySummaryOptions,
): RealtimeActivitySummary {
  if (!options.channel || !Array.isArray(activities) || !Array.isArray(members) || activities.length > 8192 || members.length > 8192 ||
      (options.names !== undefined && (!Array.isArray(options.names) || options.names.some(name => !/^[A-Za-z0-9_.:-]{1,64}$/.test(name))))) {
    throw new RealtimeConfigurationError('invalid activity summary inputs');
  }
  const byMember = new Map<string, RealtimeActivityParticipant>();
  for (const member of members) {
    if (!member.memberId || !Number.isSafeInteger(member.connectionCount) || member.connectionCount < 1 || member.connectionCount > 512 || byMember.has(member.memberId)) throw new RealtimeConfigurationError('invalid or duplicate presence member');
    if (member.memberId !== options.excludeMemberId) byMember.set(member.memberId, participant(member, options.label));
  }
  const viewers = Array.from(byMember.values());
  const names = options.names === undefined ? undefined : new Set(options.names);
  const now = Date.now();
  for (const activity of activities) {
    if (activity.channel !== options.channel || activity.memberId === options.excludeMemberId || (names && !names.has(activity.name)) || !Number.isFinite(Date.parse(activity.expiresAt)) || Date.parse(activity.expiresAt) <= now) continue;
    let member = byMember.get(activity.memberId);
    if (!member && options.includeUnknownMembers) {
      member = participant({ memberId: activity.memberId, state: {}, connectionCount: 0 }, options.label);
      byMember.set(activity.memberId, member);
    }
    if (member) member.activities.push(jsonCopy(activity));
  }
  const order = (a: RealtimeActivityParticipant, b: RealtimeActivityParticipant): number => a.memberId < b.memberId ? -1 : a.memberId > b.memberId ? 1 : 0;
  viewers.sort(order);
  const active = Array.from(byMember.values()).filter(member => member.activities.length > 0).sort(order);
  for (const member of active) member.activities.sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
  return { channel: options.channel, viewers, active, viewerCount: viewers.length, connectionCount: viewers.reduce((sum, member) => sum + member.connectionCount, 0) };
}
/** English convenience text; build localized text from summary.active when needed. */
export function formatRealtimeTypingSummary(summary: RealtimeActivitySummary, maxLabels = 1): string {
  if (!Number.isInteger(maxLabels) || maxLabels < 1 || maxLabels > 10) throw new RealtimeConfigurationError('maxLabels must be 1..10');
  const typing = summary.active.filter(member => member.activities.some(activity => activity.name === 'typing'));
  if (typing.length === 0) return '';
  const shown = typing.slice(0, maxLabels).map(member => member.label);
  const remaining = typing.length - shown.length;
  const label = remaining > 0 ? `${shown.join(', ')} and ${remaining} other${remaining === 1 ? '' : 's'}` :
    shown.length > 1 ? `${shown.slice(0, -1).join(', ')} and ${shown[shown.length - 1]}` : shown[0];
  return `${label} ${typing.length === 1 ? 'is' : 'are'} typing.`;
}

export interface RealtimePresenceDirectory {
  apply(event: RealtimePresenceEvent): void;
  snapshot(channel: string): RealtimePresenceMember[];
  reset(channel?: string): void;
}
export interface RealtimePresenceDirectoryOptions {
  maxMembers?: number;
  onChange(channel: string, members: RealtimePresenceMember[]): void;
}
/** Assemble chunked snapshots and live changes, without trusting client state as identity. */
export function createRealtimePresenceDirectory(options: RealtimePresenceDirectoryOptions): RealtimePresenceDirectory {
  const maxMembers = options.maxMembers ?? 1024;
  if (!Number.isInteger(maxMembers) || maxMembers < 1 || maxMembers > 8192 || typeof options.onChange !== 'function') throw new RealtimeConfigurationError('presence directory requires onChange and maxMembers 1..8192');
  const channels = new Map<string, Map<string, RealtimePresenceMember>>();
  const pending = new Map<string, Map<string, RealtimePresenceMember>>();
  const snapshot = (channel: string): RealtimePresenceMember[] => Array.from(channels.get(channel)?.values() ?? []).map(member => jsonCopy(member)).sort((a,b) => a.memberId < b.memberId ? -1 : a.memberId > b.memberId ? 1 : 0);
  const notify = (channel: string): void => options.onChange(channel, snapshot(channel));
  return {
    apply(event) {
      if (!event.channel || !['snapshot','joined','updated','left'].includes(event.event)) throw new RealtimeConfigurationError('invalid presence event');
      const next = new Map(event.event === 'snapshot' ? pending.get(event.channel) : channels.get(event.channel));
      const members = event.event === 'snapshot' ? event.members ?? [] : event.member ? [event.member] : [];
      if (members.length > maxMembers) throw new RealtimeConfigurationError('presence directory capacity reached');
      for (const member of members) {
        if (!member.memberId || !Number.isSafeInteger(member.connectionCount) || member.connectionCount < 1 || member.connectionCount > 512) throw new RealtimeConfigurationError('invalid presence member');
        next.set(member.memberId, jsonCopy(member));
      }
      if (event.event === 'left') {
        if (!event.memberId) throw new RealtimeConfigurationError('presence departure requires memberId');
        next.delete(event.memberId);
      } else if (event.event !== 'snapshot' && !event.member) throw new RealtimeConfigurationError('presence change requires member');
      const nextChannels = new Map(channels), nextPending = new Map(pending);
      const incomplete = event.event === 'snapshot' && event.complete !== true;
      if (incomplete) nextPending.set(event.channel, next);
      else {
        nextChannels.set(event.channel, next);
        if (event.event === 'snapshot') nextPending.delete(event.channel);
        else {
          const staged = nextPending.get(event.channel);
          if (staged) {
            const updated = new Map(staged);
            if (event.event === 'left') updated.delete(event.memberId!);
            else if (event.member) updated.set(event.member.memberId, jsonCopy(event.member));
            nextPending.set(event.channel, updated);
          }
        }
      }
      let total = 0;
      for (const stored of nextChannels.values()) total += stored.size;
      for (const stored of nextPending.values()) total += stored.size;
      const channelCount = new Set([...nextChannels.keys(), ...nextPending.keys()]).size;
      if (total > maxMembers || channelCount > maxMembers) throw new RealtimeConfigurationError('presence directory capacity reached');
      channels.clear(); pending.clear();
      for (const [channel, stored] of nextChannels) channels.set(channel, stored);
      for (const [channel, stored] of nextPending) pending.set(channel, stored);
      if (!incomplete) notify(event.channel);
    },
    snapshot,
    reset(channel) {
      if (channel !== undefined) { const changed = (channels.get(channel)?.size ?? 0) > 0; channels.delete(channel); pending.delete(channel); if (changed) notify(channel); return; }
      const changed = Array.from(channels).filter(([, members]) => members.size > 0).map(([name]) => name);
      channels.clear(); pending.clear(); for (const name of changed) notify(name);
    },
  };
}
