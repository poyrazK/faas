import type { RealtimeSignal } from './realtime-resume.js';

export interface RealtimeTemporarySignal extends RealtimeSignal {
  name: string;
  updatedAt: string;
  expiresAt: string;
}

export interface RealtimeSignalTracker {
  /** Apply a named signal; unnamed signals are ignored. */
  accept(signal: RealtimeSignal): boolean;
  snapshot(): readonly RealtimeTemporarySignal[];
  removeMember(channel: string, memberId: string): void;
  /** Clear on reconnect: temporary signals have no replay or initial snapshot. */
  clear(): void;
  dispose(): void;
}

interface Entry {
  value?: RealtimeTemporarySignal;
  updated: number;
  expires: number;
  retainUntil: number;
}

/** Track up to 256 named channel/member signals and notify when they expire.
 * Short-lived tombstones prevent delayed updates from undoing explicit clears.
 * Uses the local clock against Gregale's server-assigned expiry timestamps.
 */
export function createRealtimeSignalTracker(
  onChange: (signals: readonly RealtimeTemporarySignal[]) => void,
): RealtimeSignalTracker {
  if (typeof onChange !== 'function') throw new TypeError('signal tracker requires onChange');
  const entries = new Map<string, Entry>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  let disposed = false;

  const snapshot = (): readonly RealtimeTemporarySignal[] => {
    const now = Date.now();
    return [...entries.values()].flatMap((entry) => entry.value && entry.expires > now ? [{ ...entry.value }] : []);
  };
  const notify = (): void => { onChange(snapshot()); };
  const schedule = (): void => {
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    if (disposed || entries.size === 0) return;
    const next = Math.min(...[...entries.values()].map((entry) => entry.value ? entry.expires : entry.retainUntil));
    timer = setTimeout(() => {
      timer = undefined;
      const now = Date.now();
      let changed = false;
      for (const [key, entry] of entries) {
        if (entry.value && entry.expires <= now) { entry.value = undefined; changed = true; }
        if (entry.retainUntil <= now) entries.delete(key);
      }
      schedule();
      if (changed) notify();
    }, Math.max(1, next - Date.now()));
  };

  const clear = (): void => {
    const changed = entries.size > 0;
    entries.clear();
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    if (changed) notify();
  };

  return {
    accept(signal) {
      if (disposed || signal.name === undefined) return false;
      const updated = Date.parse(signal.updatedAt ?? ''), expires = Date.parse(signal.expiresAt ?? '');
      if (!/^[A-Za-z0-9_.:-]{1,64}$/.test(signal.name) || !Number.isFinite(updated) || !Number.isFinite(expires) ||
          expires < updated || expires - updated > 30000) throw new TypeError('invalid temporary signal');
      const key = JSON.stringify([signal.channel, signal.memberId, signal.name]);
      const previous = entries.get(key);
      if (previous && updated < previous.updated) return false;
      entries.delete(key);
      if (entries.size >= 256) entries.delete(entries.keys().next().value!);
      const now = Date.now();
      entries.set(key, {
        value: expires > updated && expires > now ? { ...signal, name: signal.name, updatedAt: signal.updatedAt!, expiresAt: signal.expiresAt! } : undefined,
        updated, expires: Math.min(expires, now + 30000), retainUntil: now + 30000,
      });
      schedule();
      notify();
      return true;
    },
    snapshot,
    removeMember(channel, memberId) {
      let changed = false;
      for (const [key, entry] of entries) {
        if (entry.value?.channel === channel && entry.value.memberId === memberId) {
          entries.set(key, { ...entry, value: undefined, updated: Math.max(entry.updated, Date.now()), retainUntil: Date.now() + 30000 });
          changed = true;
        }
      }
      schedule();
      if (changed) notify();
    },
    clear,
    dispose() { disposed = true; clear(); },
  };
}
