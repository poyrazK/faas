import { randomUUID } from 'node:crypto';
import { registerIssueSourceMaps, type IssueSourceMap } from './issue-source-maps.js';
import { loadIssueSourceMapManifest, type IssueSourceMapManifestOptions } from './issue-source-map-manifest.js';
import { issueContextLimits, type IssueBreadcrumbEntry } from './issue-context.js';
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
  const maxQueue = Math.min(1000, Math.max(1, options.maxQueue ?? 100));
  const timeout = Math.min(10000, Math.max(100, options.timeoutMs ?? 2000));
  const fetchImpl = options.fetch ?? globalThis.fetch;
  const queue: FailureEvent[] = [];
  let flushing: Promise<boolean> | undefined;
  let retry: ReturnType<typeof setTimeout> | undefined;
  let stopped = false;
  let closeDeadline = Number.POSITIVE_INFINITY;
  let dropped = 0;
  let accepted = 0;
  let mappedFrames = 0;
  let unmappedFrames = 0;

  function captureException(error: unknown, context: IssueContext = {}): string | undefined {
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
      event_id: randomUUID(), occurred_at: new Date().toISOString(), exception_type: bounded(value.name,256),
      message: bounded(value.message,2048), stack_trace: value.stack ? bounded(value.stack,16384) : undefined, frames,
      trace_id: context.trace_id, span_id: context.span_id, request_id: context.request_id,
      invocation_id: context.invocation_id, route: context.route, source_kind: context.source_kind,
      error_code: context.error_code === undefined ? undefined : bounded(context.error_code,256),
      fingerprint_override: context.fingerprint_override === undefined ? undefined : bounded(context.fingerprint_override,256),
      breadcrumbs: context.breadcrumbs?.slice(-issueContextLimits.breadcrumbs).map(entry=>({
        occurred_at:entry.occurred_at,category:entry.category,level:entry.level,message:bounded(entry.message,issueContextLimits.messageBytes),
      })),
      breadcrumbs_dropped: context.breadcrumbs===undefined?context.breadcrumbs_dropped:
        Math.min(2147483647,Math.max(0,Math.floor(context.breadcrumbs_dropped??0))+Math.max(0,context.breadcrumbs.length-issueContextLimits.breadcrumbs)),
      tags: context.tags===undefined?undefined:Object.fromEntries(Object.entries(context.tags).slice(0,issueContextLimits.tags)
        .filter(([key,value])=>key.length<=issueContextLimits.keyBytes&&typeof value==='string')
        .map(([key,value])=>[key,bounded(value,issueContextLimits.valueBytes)])),
    };
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
    if (groupingStrategy === 'cause-chain-v1') {
      while (event.causes?.length && Buffer.byteLength(JSON.stringify(event),'utf8') > 65536) {
        event.causes.pop(); event.causes_incomplete = true;
      }
    }
    queue.push(event);
    queueMicrotask(() => { void flush(); });
    return event.event_id;
  }
  async function deliver(): Promise<boolean> {
    while (queue.length) {
      if (Date.now() >= closeDeadline) return false;
      const event = queue[0]!;
      let response: Response;
      try {
        response = await fetchImpl(endpoint, { method: 'POST', redirect: 'error', signal: AbortSignal.timeout(timeout),
          headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` }, body: JSON.stringify(event) });
      } catch { return false; }
      if (response.status === 202) { queue.shift(); accepted++; }
      else if (response.status === 429 || response.status >= 500) { return false; }
      else { queue.shift(); dropped++; }
    }
    return true;
  }
  function flush(): Promise<boolean> {
    if (!flushing) flushing = deliver().finally(() => {
      flushing = undefined;
      if (queue.length && !stopped && !retry) {
        retry = setTimeout(() => { retry = undefined; void flush(); }, 1000);
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
  async function close(timeoutMs = 5000) { closeDeadline = Date.now() + Math.max(0, timeoutMs); stopped = true; if (retry) clearTimeout(retry); return flush(); }
  return { captureException, flush, close, install, wrap, stats: () => ({ queued: queue.length, accepted, dropped, mappedFrames, unmappedFrames }) };
}
