import { liveJobComplete, parseLiveJobSnapshot, type LiveJobEvent, type LiveJobSnapshot } from './live-jobs.js';

export interface LiveJobBridgeOptions {
  loadSnapshot(resourceId: string, signal: AbortSignal): Promise<unknown>;
  /** Use a retained publish with a stable key derived from the entire snapshot.
   * Publication is an invalidation, not a durable task event/audit log. */
  publish(snapshot: LiveJobSnapshot, event: LiveJobEvent, signal: AbortSignal): Promise<void>;
  pollDelayMs?: number;
  /** Renew only after authorizing and issuing a new private subscription ticket. */
  leaseMs?: number;
  requestTimeoutMs?: number;
  maxResources?: number;
  maxPolls?: number;
}
export interface LiveJobBridge {
  /** A bounded shared observer per resource, across this server's viewers. */
  watch(resourceId: string): void;
  /** Serialize concurrent reads; authorize the caller before calling this. */
  snapshot(resourceId: string): Promise<LiveJobSnapshot>;
  close(): void;
}
/** Server-side observation of native Jobs where push progress is unavailable.
 * No browser polling. Restart recovery comes from the app's durable run receipt.
 * Multiple server processes may publish duplicates; viewers always resnapshot.
 */
export function createLiveJobBridge(options: LiveJobBridgeOptions): LiveJobBridge {
  options = Object.freeze({ ...options });
  const delay = options.pollDelayMs ?? 1000, lease = options.leaseMs ?? 60000, timeout = options.requestTimeoutMs ?? 10000;
  const capacity = options.maxResources ?? 128, limit = options.maxPolls ?? 10000;
  for (const duration of [delay, lease, timeout]) if (!Number.isFinite(duration) || duration <= 0 || duration > 2147483647) throw new TypeError('Invalid bridge duration');
  for (const bound of [capacity, limit]) if (!Number.isSafeInteger(bound) || bound < 1) throw new TypeError('Invalid bridge limit');
  const observers = new Map<string, { until: number; abort: AbortController; timer?: ReturnType<typeof setTimeout>; expiry?: ReturnType<typeof setTimeout>; polls: number; last?: string }>();
  const reads = new Map<string, Promise<LiveJobSnapshot>>();
  const operations = new Set<AbortController>();
  let closed = false;
  function check(resource: string): void { if (closed || !/^[A-Za-z0-9_.:-]{1,256}$/.test(resource)) throw new Error('invalid_resource'); }
  async function bounded<T>(operation: (signal: AbortSignal) => Promise<T>): Promise<T> {
    const abort = new AbortController(); operations.add(abort);
    let reject!: (error: Error) => void;
    const cancelled = new Promise<never>((_, fail) => { reject = fail; });
    const onAbort = () => reject(new Error('bridge_request_cancelled'));
    abort.signal.addEventListener('abort', onAbort, { once: true });
    const timer = setTimeout(() => abort.abort(), timeout);
    try { return await Promise.race([operation(abort.signal), cancelled]); }
    finally { clearTimeout(timer); abort.signal.removeEventListener('abort', onAbort); operations.delete(abort); abort.abort(); }
  }
  function snapshot(resource: string): Promise<LiveJobSnapshot> {
    check(resource);
    const prior = reads.get(resource); if (prior) return prior;
    if (reads.size >= capacity) return Promise.reject(new Error('live_capacity'));
    const reading = bounded(async signal => parseLiveJobSnapshot(await options.loadSnapshot(resource, signal), resource));
    reads.set(resource, reading);
    void reading.finally(() => { if (reads.get(resource) === reading) reads.delete(resource); }).catch(() => {});
    return reading;
  }
  function remove(resource: string): void {
    const state = observers.get(resource); if (!state) return;
    clearTimeout(state.timer); clearTimeout(state.expiry); state.abort.abort(); observers.delete(resource);
  }
  function watch(resource: string): void {
    check(resource);
    const old = observers.get(resource);
    if (old) { old.until = performance.now() + lease; clearTimeout(old.expiry); old.expiry = setTimeout(() => remove(resource), lease); return; }
    if (observers.size >= capacity) throw new Error('live_capacity');
    const state = { until: performance.now() + lease, abort: new AbortController(), polls: 0 } as NonNullable<ReturnType<typeof observers.get>>;
    observers.set(resource, state); state.expiry = setTimeout(() => remove(resource), lease);
    async function tick(): Promise<void> {
      const active = () => !closed && !state.abort.signal.aborted && observers.get(resource) === state && performance.now() < state.until;
      if (!active() || ++state.polls > limit) { remove(resource); return; }
      let done = false;
      try {
        const value = await snapshot(resource); if (!active()) return;
        const content = JSON.stringify(value);
        if (content !== state.last) {
          const kind = value.report === 'ready' ? 'report_ready' : liveJobComplete(value) ? 'completed' : value.failed > 0 || value.failures.length ? 'task_failed' : 'progress';
          await bounded(signal => options.publish(value, { version: 1, resource_id: resource, kind }, signal));
          if (!active()) return;
          state.last = content;
        }
        done = liveJobComplete(value);
      } catch { /* Retry observation/publish within the lease; snapshots remain authoritative. */ }
      if (done || !active()) { remove(resource); return; }
      state.timer = setTimeout(() => { void tick(); }, delay);
    }
    void tick();
  }
  return { watch, snapshot, close() { closed = true; for (const resource of observers.keys()) remove(resource); for (const operation of operations) operation.abort(); } };
}
