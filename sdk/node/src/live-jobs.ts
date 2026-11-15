/** Typed, application-authorized views of Jobs and file pipelines (preview).
 * Notifications invalidate a view; only the authenticated snapshot loader can
 * change it. Worker messages are never evidence of successful completion.
 */
export type LiveJobStatus = 'awaiting_storage_event' | 'submission_unresolved' | 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'dead_letter';
export interface LiveJobSnapshot {
  readonly waiting?: { readonly kind: 'approval' | 'input'; readonly gate_id: string; readonly name: string };
  readonly version: 1;
  readonly resource_id: string;
  readonly run_id: string | null;
  readonly status: LiveJobStatus;
  readonly total: number;
  readonly succeeded: number;
  readonly failed: number;
  readonly cancelled: number;
  readonly running: number;
  /** ready means the application has verified and materialized the report. */
  readonly report: 'none' | 'pending' | 'ready' | 'unavailable';
  readonly failures: readonly { readonly input_id: string; readonly code: string }[];
}
export interface LiveJobEvent {
  readonly version: 1;
  readonly resource_id: string;
  readonly kind: 'progress' | 'task_failed' | 'completed' | 'report_ready';
}
export interface LiveJobSink {
  ready(): void;
  changed(event: unknown): void;
  disconnected(): void;
  failed(): void;
}
/** Subscribe before ready(). Invoke ready again after reconnect or a history gap.
 * Bind this source to one authenticated principal and one private resource.
 */
export type LiveJobSource = (sink: LiveJobSink, signal: AbortSignal) => (() => void);
export interface LiveJobOptions {
  resourceId: string;
  /** Must authorize the current application user and verify native Jobs state. */
  loadSnapshot(signal: AbortSignal): Promise<unknown>;
  source: LiveJobSource;
  timeoutMs: number;
  signal?: AbortSignal;
  maxSnapshots?: number;
}
export interface LiveJobView {
  readonly connection: 'idle' | 'connecting' | 'live' | 'reconnecting' | 'complete' | 'error';
  readonly snapshot?: LiveJobSnapshot;
  readonly error?: string;
}
export interface LiveJob {
  getView(): LiveJobView;
  subscribe(listener: () => void): () => void;
  /** Coalesces intermediate views; never buffers an unbounded event history. */
  updates(): AsyncIterableIterator<LiveJobSnapshot>;
  close(): void;
}
export class LiveJobError extends Error {
  constructor(readonly code: string) { super(code); this.name = 'LiveJobError'; }
}
const statuses = new Set(['awaiting_storage_event', 'submission_unresolved', 'queued', 'running', 'succeeded', 'failed', 'cancelled', 'dead_letter']);
const terminal = new Set(['succeeded', 'failed', 'cancelled', 'dead_letter']);
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new LiveJobError('invalid_snapshot');
  return value as Record<string, unknown>;
}
function id(value: unknown): value is string { return typeof value === 'string' && /^[A-Za-z0-9_.:-]{1,256}$/.test(value); }
/** Copy only documented fields; credentials, result payloads and URLs stay out. */
export function parseLiveJobSnapshot(value: unknown, resourceId: string): LiveJobSnapshot {
  const v = object(value);
  if (!id(resourceId) || v.version !== 1 || v.resource_id !== resourceId || typeof v.status !== 'string' || !statuses.has(v.status) ||
      (v.run_id !== null && !id(v.run_id)) || typeof v.report !== 'string' || !['none', 'pending', 'ready', 'unavailable'].includes(v.report)) throw new LiveJobError('invalid_snapshot');
  for (const key of ['total', 'succeeded', 'failed', 'cancelled', 'running']) {
    if (!Number.isSafeInteger(v[key]) || (v[key] as number) < 0) throw new LiveJobError('invalid_snapshot');
  }
  if ((v.succeeded as number) + (v.failed as number) + (v.cancelled as number) + (v.running as number) > (v.total as number) ||
      (terminal.has(String(v.status)) && v.run_id === null) || (v.report === 'ready' && !terminal.has(String(v.status))) ||
      !Array.isArray(v.failures) || v.failures.length > 1000) throw new LiveJobError('invalid_snapshot');
  const failures = v.failures.map(item => {
    const f = object(item);
    if (!id(f.input_id) || !id(f.code)) throw new LiveJobError('invalid_snapshot');
    return Object.freeze({ input_id: f.input_id, code: f.code });
  });
  const waiting = v.waiting as LiveJobSnapshot['waiting'];
  if (waiting !== undefined && (!waiting || !['approval', 'input'].includes(waiting.kind) || !/^[a-f0-9]{64}$/.test(waiting.gate_id) || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(waiting.name) || !['running', 'queued'].includes(v.status as string))) throw new LiveJobError('invalid_snapshot');
  return Object.freeze({ ...(waiting ? { waiting: Object.freeze({ kind: waiting.kind, gate_id: waiting.gate_id, name: waiting.name }) } : {}), version: 1, resource_id: resourceId, run_id: v.run_id as string | null, status: v.status as LiveJobStatus,
    total: v.total as number, succeeded: v.succeeded as number, failed: v.failed as number, cancelled: v.cancelled as number,
    running: v.running as number, report: v.report as LiveJobSnapshot['report'], failures: Object.freeze(failures) });
}
export function parseLiveJobEvent(value: unknown, resourceId: string): LiveJobEvent {
  const v = object(value);
  if (!id(resourceId) || v.version !== 1 || v.resource_id !== resourceId || typeof v.kind !== 'string' || !['progress', 'task_failed', 'completed', 'report_ready'].includes(v.kind)) throw new LiveJobError('invalid_event');
  return Object.freeze({ version: 1, resource_id: resourceId, kind: v.kind as LiveJobEvent['kind'] });
}
export function liveJobComplete(snapshot: LiveJobSnapshot): boolean {
  return terminal.has(snapshot.status) && snapshot.report !== 'pending';
}

/** Lazy shared controller. Create a NEW controller when the user/session changes.
 * Closing or losing authorization clears its snapshot and fences late callbacks.
 */
export function createLiveJob(options: LiveJobOptions): LiveJob {
  options = Object.freeze({ ...options });
  if (!id(options.resourceId) || !Number.isFinite(options.timeoutMs) || options.timeoutMs <= 0 || options.timeoutMs > 2147483647 ||
      !Number.isSafeInteger(options.maxSnapshots ?? 10000) || (options.maxSnapshots ?? 10000) < 1) throw new TypeError('Invalid live job options');
  const listeners = new Set<() => void>();
  let view: LiveJobView = Object.freeze({ connection: 'idle' }), generation = 0, closed = false;
  let stop: (() => void) | undefined;
  function emit(next: LiveJobView): void { view = Object.freeze(next); for (const listener of [...listeners]) listener(); }
  function start(): void {
    const epoch = ++generation, controller = new AbortController(), deadline = performance.now() + options.timeoutMs;
    let cleanup: (() => void) | undefined, dirty = false, loading = false, count = 0, connected = false;
    const active = () => epoch === generation && !controller.signal.aborted;
    const end = () => { if (epoch !== generation) return; generation++; clearTimeout(timer); options.signal?.removeEventListener('abort', abort); controller.abort(); cleanup?.(); stop = undefined; };
    const fail = (code: string) => { if (!active()) return; end(); emit({ connection: 'error', error: code }); };
    const abort = () => fail('cancelled');
    const timer = setTimeout(() => fail('deadline_exceeded'), options.timeoutMs);
    stop = end;
    emit({ connection: 'connecting' });
    if (options.signal?.aborted) { abort(); return; }
    options.signal?.addEventListener('abort', abort, { once: true });
    async function refresh(): Promise<void> {
      dirty = true;
      if (loading) return;
      loading = true;
      try {
        while (dirty && active()) {
          dirty = false;
          if (++count > (options.maxSnapshots ?? 10000)) { fail('snapshot_limit'); break; }
          const snapshot = parseLiveJobSnapshot(await options.loadSnapshot(controller.signal), options.resourceId);
          if (!active()) return;
          if (performance.now() >= deadline) { fail('deadline_exceeded'); return; }
          // A resource is bound to its accepted run. Never adopt a different run
          // or roll a verified terminal observation back to a pending state.
          const prior = view.snapshot;
          if (prior?.run_id && snapshot.run_id !== prior.run_id || prior && terminal.has(prior.status) && snapshot.status !== prior.status) throw new LiveJobError('snapshot_regressed');
          if (liveJobComplete(snapshot)) { end(); emit({ connection: 'complete', snapshot }); return; }
          emit({ connection: connected ? 'live' : 'reconnecting', snapshot });
        }
      } catch { fail('snapshot_failed'); } finally { loading = false; }
    }
    try {
      const release = options.source({
        ready() { if (active()) { connected = true; void refresh(); } },
        changed(event) { if (!active()) return; try { parseLiveJobEvent(event, options.resourceId); } catch { return; } void refresh(); },
        disconnected() { if (active()) { connected = false; emit({ connection: 'reconnecting', snapshot: view.snapshot }); } },
        failed() { fail('subscription_failed'); },
      }, controller.signal);
      cleanup = release;
      if (!active()) release();
    } catch { fail('subscription_failed'); }
  }
  const live: LiveJob = {
    getView: () => view,
    subscribe(listener) {
      listeners.add(listener);
      if (closed) { listeners.delete(listener); return () => {}; }
      if (listeners.size === 1 && view.connection !== 'complete' && view.connection !== 'error') start();
      let removed = false;
      return () => { if (removed) return; removed = true; listeners.delete(listener); if (!listeners.size && stop) { stop(); emit({ connection: 'idle' }); } };
    },
    async *updates() {
      let wake: (() => void) | undefined, latest = view;
      const unsubscribe = live.subscribe(() => { latest = view; wake?.(); wake = undefined; });
      latest = view;
      let previous: LiveJobSnapshot | undefined;
      try {
        for (;;) {
          const current = latest;
          if (current.snapshot && current.snapshot !== previous) { previous = current.snapshot; yield previous; }
          if (current.connection === 'error') throw new LiveJobError(current.error ?? 'closed');
          if (current.connection === 'complete') return;
          if (latest !== current) continue;
          await new Promise<void>(resolve => { wake = resolve; });
        }
      } finally { unsubscribe(); }
    },
    close() { if (closed) return; closed = true; stop?.(); emit({ connection: 'error', error: 'closed' }); listeners.clear(); },
  };
  return live;
}
