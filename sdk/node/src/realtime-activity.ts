import { RealtimeConfigurationError, type RealtimeSignal } from './realtime-resume.js';

export interface RealtimeActivity extends RealtimeSignal {
  name: string;
  updatedAt: string;
  expiresAt: string;
}
export interface RealtimeActivityTrackerOptions {
  /** Maximum identities, including short-lived clear/expiry tombstones; default 1024. */
  maxEntries?: number;
  onChange(activities: RealtimeActivity[]): void;
}
export interface RealtimeActivityTracker {
  /** Use from onSignal. Unnamed, duplicate, and stale updates return false. Expired newer updates remove activity. */
  apply(signal: RealtimeSignal): boolean;
  /** Return an isolated snapshot of current, unexpired activity. */
  snapshot(): RealtimeActivity[];
  /** Remove a departed sender's activity while retaining ordering markers. */
  removeMember(channel: string, memberId: string): void;
  /** Clear activity and ordering memory for one channel, or all channels. */
  reset(channel?: string): void;
  /** Stop timers, clear activity, and reject subsequent updates. */
  dispose(): void;
}
interface ActivityEntry {
  activity?: RealtimeActivity;
  revision: string;
  forgetAt: number;
}
function activityTimestamp(value: string): { milliseconds: number; revision: string } {
  const match = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.(\d{1,9}))?(?:Z|[+-]\d{2}:\d{2})$/.exec(value);
  const milliseconds = Date.parse(value);
  if (!match || !Number.isFinite(milliseconds)) throw new RealtimeConfigurationError('activity requires RFC3339 timestamps');
  // Preserve server microseconds when updates arrive within the same millisecond.
  const revision = new Date(milliseconds).toISOString().slice(0, 19) + '.' + (match[1] ?? '').padEnd(9, '0') + 'Z';
  return { milliseconds, revision };
}
function copyActivity(activity: RealtimeActivity): RealtimeActivity {
  return { ...activity, data: JSON.parse(JSON.stringify(activity.data)) as unknown };
}
/** Maintain expiring named channel signals without replay or durable state. */
export function createRealtimeActivityTracker(options: RealtimeActivityTrackerOptions): RealtimeActivityTracker {
  const maxEntries = options.maxEntries ?? 1024;
  if (!Number.isInteger(maxEntries) || maxEntries < 1 || maxEntries > 8192 || typeof options.onChange !== 'function') {
    throw new RealtimeConfigurationError('activity tracker requires onChange and maxEntries 1..8192');
  }
  const entries = new Map<string, ActivityEntry>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let disposed = false;
  const stopTimer = (): void => { if (timer !== undefined) clearTimeout(timer); timer = undefined; };
  const prune = (now: number): boolean => {
    let changed = false;
    for (const [key, entry] of entries) {
      if (entry.activity && Date.parse(entry.activity.expiresAt) <= now) { entry.activity = undefined; changed = true; }
      if (!entry.activity && entry.forgetAt <= now) entries.delete(key);
    }
    return changed;
  };
  const current = (): RealtimeActivity[] => Array.from(entries.values())
    .flatMap(entry => entry.activity ? [copyActivity(entry.activity)] : []);
  const notify = (): void => { options.onChange(current()); };
  const schedule = (): void => {
    stopTimer();
    if (disposed || entries.size === 0) return;
    let next = Infinity;
    for (const entry of entries.values()) next = Math.min(next, entry.activity ? Date.parse(entry.activity.expiresAt) : entry.forgetAt);
    timer = setTimeout(() => {
      timer = undefined;
      const changed = prune(Date.now());
      schedule();
      if (changed) notify();
    }, Math.max(1, next - Date.now()));
  };
  return {
    apply(signal) {
      if (disposed) throw new RealtimeConfigurationError('activity tracker is disposed');
      if (signal.name === undefined) return false;
      if (typeof signal.channel !== 'string' || !signal.channel || typeof signal.memberId !== 'string' || !signal.memberId ||
          new TextEncoder().encode(signal.channel).byteLength > 256 || signal.memberId.length > 128 || !/^[A-Za-z0-9_.:-]{1,64}$/.test(signal.name) ||
          typeof signal.updatedAt !== 'string' || typeof signal.expiresAt !== 'string') {
        throw new RealtimeConfigurationError('invalid named activity signal');
      }
      const updated = activityTimestamp(signal.updatedAt), expires = activityTimestamp(signal.expiresAt);
      const now = Date.now();
      if (expires.revision < updated.revision || expires.revision > new Date(updated.milliseconds + 30000).toISOString().slice(0, 19) + updated.revision.slice(19)) throw new RealtimeConfigurationError('activity TTL must be 0..30000 ms');
      // Bound ordering memory: packets older than the maximum TTL cannot revive expired state.
      if (updated.milliseconds < now - 30000 || updated.milliseconds > now + 30000) return false;
      let encoded: string | undefined;
      try { encoded = JSON.stringify(signal.data); } catch { throw new RealtimeConfigurationError('activity data must be JSON'); }
      if (encoded === undefined || new TextEncoder().encode(encoded).byteLength > 2048) throw new RealtimeConfigurationError('activity data must be within 2048 JSON bytes');
      const key = JSON.stringify([signal.channel, signal.memberId, signal.name]);
      let changed = prune(now);
      const previous = entries.get(key);
      if (previous && updated.revision <= previous.revision) { schedule(); if (changed) notify(); return false; }
      const clear = expires.revision === updated.revision || expires.milliseconds <= now;
      if (!previous && entries.size >= maxEntries) {
        schedule(); if (changed) notify();
        throw new RealtimeConfigurationError('activity tracker identity capacity reached');
      }
      let activity: RealtimeActivity | undefined;
      if (!clear) {
        activity = { channel: signal.channel, memberId: signal.memberId, name: signal.name, updatedAt: signal.updatedAt, expiresAt: signal.expiresAt, data: JSON.parse(encoded) as unknown };
      }
      entries.set(key, { activity, revision: updated.revision, forgetAt: Math.max(now + 30000, updated.milliseconds + 30000, expires.milliseconds) });
      changed = changed || activity !== undefined || previous?.activity !== undefined;
      schedule(); if (changed) notify();
      return true;
    },
    snapshot() {
      const changed = prune(Date.now()); schedule();
      const result = current(); if (changed) notify(); return result;
    },
    removeMember(channel, memberId) {
      if (disposed) return;
      const now = Date.now();
      const revision = activityTimestamp(new Date(now).toISOString()).revision;
      let changed = false;
      for (const [key, entry] of entries) {
        const identity = JSON.parse(key) as string[];
        if (identity[0] !== channel || identity[1] !== memberId) continue;
        changed = changed || entry.activity !== undefined;
        entry.activity = undefined;
        if (revision > entry.revision) entry.revision = revision;
        entry.forgetAt = Math.max(entry.forgetAt, now + 30000);
      }
      schedule(); if (changed) notify();
    },
    reset(channel) {
      if (disposed) return;
      let changed = false;
      for (const [key, entry] of entries) {
        if (channel === undefined || (JSON.parse(key) as string[])[0] === channel) {
          changed = changed || entry.activity !== undefined; entries.delete(key);
        }
      }
      schedule(); if (changed) notify();
    },
    dispose() {
      if (disposed) return;
      const changed = Array.from(entries.values()).some(entry => entry.activity !== undefined);
      disposed = true; stopTimer(); entries.clear(); if (changed) notify();
    },
  };
}
