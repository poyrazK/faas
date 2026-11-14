import { randomUUID } from 'node:crypto';
import { registerIssueSourceMaps, type IssueSourceMap } from './issue-source-maps.js';
import { loadIssueSourceMapManifest, type IssueSourceMapManifestOptions } from './issue-source-map-manifest.js';
import { issueContextLimits, issueTagKeys, type IssueBreadcrumbEntry } from './issue-context.js';
export type { IssueSourceMap } from './issue-source-maps.js';

export interface IssueContext {
  breadcrumbs?: readonly IssueBreadcrumbEntry[];
  breadcrumbs_dropped?: number;
  tags?: Readonly<Record<string,string>>;
  trace_id?: string;
  span_id?: string;
  request_id?: string;
  invocation_id?: string;
  route?: string;
  http_status?: number;
  source_kind?: 'exception' | 'worker';
  /** Stable application error code; affects failure-site-v1 grouping only. */
  error_code?: string;
  fingerprint_override?: string;
}
export interface IssueReporterOptions {
  baseURL: string;
  app: string;
  token: string;
  maxQueue?: number;
  timeoutMs?: number;
  fetch?: typeof globalThis.fetch;
  groupingStrategy?: 'legacy' | 'failure-site-v1' | 'cause-chain-v1';
  /** Deployment-local map artifacts; requires failure-site-v1 or cause-chain-v1 grouping. */
  sourceMaps?: readonly IssueSourceMap[];
  /** Build manifest verified and loaded once against the running deployment. */
  sourceMapManifest?: IssueSourceMapManifestOptions;
  /** Request mapping with immutable artifacts published for the reporting deployment. */
  symbolicationStrategy?: 'deployment-source-maps-v1';
}
function bounded(value: string, limit: number): string {
 const bytes=Buffer.from(value,'utf8');let cut=Math.min(bytes.length,limit);
 while(cut<bytes.length && (bytes[cut]!&0xc0)===0x80) cut--;
 return bytes.subarray(0,cut).toString('utf8');
}
function correlationID(value: string | undefined, length: number): string | undefined {
  if (value === undefined) return undefined;
  const normalized = value.toLowerCase();
  return normalized.length === length && /^[0-9a-f]+$/.test(normalized) && /[1-9a-f]/.test(normalized) ? normalized : undefined;
}
function invocationID(value: string | undefined): string | undefined {
  if (value === undefined) return undefined;
  return /^(?:[0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/i.test(value) &&
    /[1-9a-f]/i.test(value) ? value : undefined;
}
function validBreadcrumbTime(value: unknown, occurredAt: number): value is string {
  if (typeof value !== 'string' || value.length > 64) return false;
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!match) return false;
  const year = Number(match[1]), month = Number(match[2]), day = Number(match[3]);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (year === 0 || month < 1 || month > 12 || day < 1 || day > days[month - 1]! ||
      Number(match[4]) > 23 || Number(match[5]) > 59 || Number(match[6]) > 59) return false;
  if (match[7] !== 'Z' && (Number(match[7]!.slice(1, 3)) > 23 || Number(match[7]!.slice(4)) > 59)) return false;
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) && timestamp !== Date.parse('0001-01-01T00:00:00Z') && timestamp <= occurredAt + 300000;
}
interface FailureEvent extends IssueContext {
  event_id: string;
  occurred_at: string;
  exception_type: string;
  message: string;
  stack_trace?: string;
  frames: Array<{ file: string; function: string; line: number; column?: number; in_app: boolean }>;
  grouping_strategy?: 'failure-site-v1' | 'cause-chain-v1';
  symbolication_strategy?: 'deployment-source-maps-v1';
  causes?: Array<{exception_type: string; message: string; error_code?: string; frames: FailureEvent['frames']; frame_order: 'newest-first'}>;
  causes_incomplete?: boolean;
  frame_order?: 'newest-first';
}

/** Captures exceptions independently of trace sampling. The credential must
 * be a deployment-bound issue ingest token, never an owner API key. */
export function createIssueReporter(options: IssueReporterOptions) {
  const base = new URL(options.baseURL);
  if (!['https:', 'http:'].includes(base.protocol) || base.username || base.password || base.search || base.hash) {
    throw new Error('Issue baseURL must be an HTTP(S) origin without credentials');
  }
  if (!options.token.startsWith('g_issue_')) throw new Error('A Gregale issue ingest token is required');
  if (options.groupingStrategy !== undefined && !['legacy', 'failure-site-v1', 'cause-chain-v1'].includes(options.groupingStrategy)) {
    throw new Error('Invalid issue grouping strategy');
  }
  if ((options.sourceMaps !== undefined || options.sourceMapManifest !== undefined) && !['failure-site-v1', 'cause-chain-v1'].includes(options.groupingStrategy ?? '')) {
    throw new Error('Issue source maps require failure-site-v1 or cause-chain-v1 grouping');
  }
  if (options.sourceMaps !== undefined && options.sourceMapManifest !== undefined) {
    throw new Error('Configure sourceMaps or sourceMapManifest, not both');
  }
  if (options.symbolicationStrategy !== undefined && (options.symbolicationStrategy !== 'deployment-source-maps-v1' ||
      !['failure-site-v1', 'cause-chain-v1'].includes(options.groupingStrategy ?? '') ||
      options.sourceMaps !== undefined || options.sourceMapManifest !== undefined)) {
    throw new Error('Server symbolication requires failure-site-v1 or cause-chain-v1 without local source maps');
  }
  const serverSymbolication = options.symbolicationStrategy;
  const sourceMaps = registerIssueSourceMaps(options.sourceMapManifest === undefined
    ? options.sourceMaps : loadIssueSourceMapManifest(options.sourceMapManifest));
  const groupingStrategy = options.groupingStrategy;
  const token = options.token;
  const endpoint = new URL(`/v1/apps/${encodeURIComponent(options.app)}/issue-events`, base);
  if (options.maxQueue !== undefined && !Number.isFinite(options.maxQueue)) throw new Error('Invalid issue queue size');
  if (options.timeoutMs !== undefined && !Number.isFinite(options.timeoutMs)) throw new Error('Invalid issue timeout');
  const maxQueue = Math.min(1000, Math.max(1, Math.floor(options.maxQueue ?? 100)));
  const timeout = Math.min(10000, Math.max(100, Math.floor(options.timeoutMs ?? 2000)));
  const fetchImpl = options.fetch ?? globalThis.fetch;
  const queue: FailureEvent[] = [];
  let flushing: Promise<boolean> | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let retryNotBefore = 0;
  let stopped = false;
  let closeDeadline = Number.POSITIVE_INFINITY;
  let inFlight: AbortController | undefined;
  let dropped = 0;
  let accepted = 0;
  let mappedFrames = 0;
  let unmappedFrames = 0;

  function captureException(error: unknown, context: IssueContext = {}): string | undefined {
    try { return capture(error, context); } catch { dropped++; return undefined; }
  }
  function capture(error: unknown, context: IssueContext): string | undefined {
    if (stopped || queue.length >= maxQueue) { dropped++; return undefined; }
    const value = error instanceof Error ? error : new Error(String(error));
    function framesFor(value: Error): FailureEvent['frames'] {
      const frames: FailureEvent['frames'] = [];
      for (const line of (value.stack ?? '').split('\n').slice(1)) {
        const match = line.match(/^\s*at\s+(?:(.*?)\s+\()?(.+?):(\d+):(\d+)\)?$/);
        if (match) {
          const generated = { file: match[2]!, function: match[1] ?? '<anonymous>',
            line: Number(match[3]), ...(serverSymbolication ? { column: Number(match[4]) } : {}),
            in_app: !match[2]!.startsWith('node:') && !match[2]!.includes('/node_modules/') };
          const original = sourceMaps.resolve(generated, Number(match[4]));
          if (original) mappedFrames++;
          else if (sourceMaps.enabled && generated.in_app) unmappedFrames++;
          const frame = original ?? generated;
          frames.push({ ...frame, file: bounded(frame.file,512), function: bounded(frame.function,512) });
        }
        if (frames.length === 32) break;
      }
      return frames;
    }
    const frames = framesFor(value);
    const event: FailureEvent = {
      event_id: randomUUID(), occurred_at: new Date().toISOString(), exception_type: bounded(typeof value.name === 'string' && value.name.trim() ? value.name : 'Error',256),
      message: bounded(value.message,2048), stack_trace: value.stack ? bounded(value.stack,16384) : undefined, frames,
      trace_id: correlationID(context.trace_id, 32), span_id: correlationID(context.span_id, 16),
      request_id: context.request_id === undefined ? undefined : bounded(context.request_id,256),
      invocation_id: invocationID(context.invocation_id), route: context.route === undefined ? undefined : bounded(context.route,256), source_kind: context.source_kind,
      http_status: Number.isInteger(context.http_status) && context.http_status! >= 400 && context.http_status! <= 599 ? context.http_status : undefined,
      error_code: context.error_code === undefined ? undefined : bounded(context.error_code,256),
      fingerprint_override: context.fingerprint_override === undefined ? undefined : bounded(context.fingerprint_override,256),
      tags: context.tags===undefined?undefined:Object.fromEntries(Object.entries(context.tags)
        .filter(([key,value])=>issueTagKeys.includes(key)&&typeof value==='string'&&!value.includes('\0'))
        .slice(0,issueContextLimits.tags)
        .map(([key,value])=>[key,bounded(value,issueContextLimits.valueBytes)])),
    };
    let crumbsDropped = Number.isFinite(context.breadcrumbs_dropped) ? Math.max(0, Math.floor(context.breadcrumbs_dropped!)) : 0;
    if (context.breadcrumbs !== undefined) {
      crumbsDropped += Math.max(0, context.breadcrumbs.length - issueContextLimits.breadcrumbs);
      const crumbs: IssueBreadcrumbEntry[] = [];
      for (const entry of context.breadcrumbs.slice(-issueContextLimits.breadcrumbs)) {
        if (!entry || !validBreadcrumbTime(entry.occurred_at, Date.parse(event.occurred_at)) ||
            !['http','db','cache','workflow','log','custom'].includes(entry.category) ||
            !['debug','info','warning','error'].includes(entry.level) || typeof entry.message !== 'string' || entry.message.includes('\0')) {
          crumbsDropped++;
          continue;
        }
        crumbs.push({occurred_at:entry.occurred_at, category:entry.category, level:entry.level,
          message:bounded(entry.message,issueContextLimits.messageBytes)});
      }
      if (crumbs.length) event.breadcrumbs = crumbs;
    }
    if (crumbsDropped) event.breadcrumbs_dropped = Math.min(2147483647, crumbsDropped);
    if (groupingStrategy === 'failure-site-v1' || groupingStrategy === 'cause-chain-v1') {
      event.grouping_strategy = groupingStrategy;
      event.frame_order = 'newest-first';
    }
    if (serverSymbolication) event.symbolication_strategy = serverSymbolication;
    if (groupingStrategy === 'cause-chain-v1') {
      const seen = new Set<Error>([value]);
      let current: Error = value;
      event.causes = [];
      try {
        while (true) {
          if (current instanceof AggregateError && current.errors.length > 0) { event.causes_incomplete = true; break; }
          const cause: unknown = current.cause;
          if (cause === undefined) break;
          if (!(cause instanceof Error) || seen.has(cause) || event.causes.length === 8) { event.causes_incomplete = true; break; }
          seen.add(cause);
          const code: unknown = (cause as Error & {code?: unknown}).code;
          event.causes.push({exception_type: bounded(cause.name || 'Error',256), message: bounded(cause.message,2048),
            error_code: typeof code === 'string' ? bounded(code,256) : undefined, frames: framesFor(cause), frame_order: 'newest-first'});
          current = cause;
        }
      } catch { event.causes_incomplete = true; }
    }
    // Bound grouping evidence without looking at optional context, so adding
    // breadcrumbs, tags or correlation IDs cannot change the fingerprint.
    const optional = {breadcrumbs:event.breadcrumbs, breadcrumbs_dropped:event.breadcrumbs_dropped, tags:event.tags,
      request_id:event.request_id, trace_id:event.trace_id, span_id:event.span_id, invocation_id:event.invocation_id, route:event.route, http_status:event.http_status};
    event.breadcrumbs = undefined; event.breadcrumbs_dropped = undefined; event.tags = undefined;
    event.request_id = undefined; event.trace_id = undefined; event.span_id = undefined; event.invocation_id = undefined; event.route = undefined;
    event.http_status = undefined;
    const oversized = (limit = 65536) => Buffer.byteLength(JSON.stringify(event),'utf8') > limit;
    while (event.causes?.length && oversized(61440)) { event.causes.pop(); event.causes_incomplete = true; }
    if (oversized(61440)) event.stack_trace = undefined;
    while (event.frames.length > 1 && oversized(61440)) event.frames.pop();
    if (oversized(61440)) { dropped++; return undefined; }
    Object.assign(event, optional);
    event.breadcrumbs = optional.breadcrumbs;
    while (event.breadcrumbs?.length && oversized()) {
      event.breadcrumbs = event.breadcrumbs.slice(1);
      event.breadcrumbs_dropped = Math.min(2147483647,(event.breadcrumbs_dropped ?? 0) + 1);
    }
    if (oversized()) event.tags = undefined;
    if (oversized()) { dropped++; return undefined; }
    queue.push(event);
    queueMicrotask(() => { void flush(); });
    return event.event_id;
  }
  async function deliver(): Promise<boolean> {
    while (queue.length) {
      if (Date.now() >= closeDeadline) return false;
      if (Date.now() < retryNotBefore) return false;
      const event = queue[0]!;
      let response: Response;
      const controller = new AbortController();
      inFlight = controller;
      try {
        response = await fetchImpl(endpoint, { method: 'POST', redirect: 'error', signal: AbortSignal.any([controller.signal, AbortSignal.timeout(timeout)]),
          headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` }, body: JSON.stringify(event) });
      } catch { retryNotBefore = Date.now() + 1000; return false; }
      finally { if (inFlight === controller) inFlight = undefined; }
      if (response.status === 202) { queue.shift(); accepted++; }
      else if (response.status === 429 || (response.status === 503 && response.headers.has('Retry-After'))) {
        const header = response.headers.get('Retry-After');
        const seconds = header && /^\d+$/.test(header) ? Number(header) : NaN;
        const date = header ? Date.parse(header) : NaN;
        const delay = Number.isFinite(seconds) ? seconds * 1000 : Number.isFinite(date) ? date - Date.now() : 60000;
        retryNotBefore = Date.now() + Math.min(86400000, Math.max(1000, delay));
        return false;
      }
      else if (response.status >= 500) { retryNotBefore = Date.now() + 1000; return false; }
      else { queue.shift(); dropped++; }
    }
    return dropped === 0;
  }
  function flush(): Promise<boolean> {
    if (!flushing) flushing = deliver().finally(() => {
      flushing = undefined;
      if (queue.length && !stopped && !retry) {
        retry = setTimeout(() => { retry = undefined; void flush(); }, Math.max(1000, retryNotBefore - Date.now()));
        retry.unref();
      }
    });
    return flushing;
  }
  /** Observes fatal exceptions without changing Node's normal exit behavior.
   * Delivery on a process crash remains best effort; wrap handlers for normal
   * request/worker failures and flush during graceful shutdown. */
  function install() {
    const monitor = (error: Error) => { try { captureException(error); } catch { dropped++; } };
    process.on('uncaughtExceptionMonitor', monitor);
    return () => process.off('uncaughtExceptionMonitor', monitor);
  }
  async function wrap<T>(handler: () => Promise<T>, context: IssueContext = {}): Promise<T> {
    try { return await handler(); } catch (error) { try { captureException(error, context); } catch { dropped++; } throw error; }
  }
  async function close(timeoutMs = 5000) {
    if (!Number.isFinite(timeoutMs)) throw new Error('Invalid issue close timeout');
    closeDeadline = Math.min(closeDeadline, Date.now() + Math.max(0, timeoutMs));
    stopped = true;
    if (retry) { clearTimeout(retry); retry = undefined; }
    async function drain(): Promise<boolean> {
      for (;;) {
        const result = await flush();
        if (!queue.length || result) return result;
        const remaining = closeDeadline - Date.now();
        if (remaining <= 0) return false;
        await new Promise<void>(resolve => setTimeout(resolve, Math.min(remaining, Math.max(1000, retryNotBefore - Date.now()))));
      }
    }
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      return await Promise.race([drain(), new Promise<boolean>(resolve => {
        timer = setTimeout(() => { inFlight?.abort(); resolve(false); }, Math.min(2147483647, Math.max(0, closeDeadline - Date.now())));
      })]);
    } finally { if (timer) clearTimeout(timer); }
  }
  return { captureException, flush, close, install, wrap, stats: () => ({ queued: queue.length, accepted, dropped, mappedFrames, unmappedFrames }) };
}
