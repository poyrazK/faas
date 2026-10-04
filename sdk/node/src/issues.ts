import { randomUUID } from 'node:crypto';

export interface IssueContext {
  trace_id?: string;
  span_id?: string;
  request_id?: string;
  invocation_id?: string;
  route?: string;
  source_kind?: 'exception' | 'worker';
}
export interface IssueReporterOptions {
  baseURL: string;
  app: string;
  token: string;
  maxQueue?: number;
  timeoutMs?: number;
  fetch?: typeof globalThis.fetch;
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
  frames: Array<{ file: string; function: string; line: number; in_app: boolean }>;
}

/** Captures exceptions independently of trace sampling. The credential must
 * be a deployment-bound issue ingest token, never an owner API key. */
export function createIssueReporter(options: IssueReporterOptions) {
  const base = new URL(options.baseURL);
  if (!['https:', 'http:'].includes(base.protocol) || base.username || base.password || base.search || base.hash) {
    throw new Error('Issue baseURL must be an HTTP(S) origin without credentials');
  }
  if (!options.token.startsWith('g_issue_')) throw new Error('A Gregale issue ingest token is required');
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

  function captureException(error: unknown, context: IssueContext = {}): string | undefined {
    if (stopped || queue.length >= maxQueue) { dropped++; return undefined; }
    const value = error instanceof Error ? error : new Error(String(error));
    const frames: FailureEvent['frames'] = [];
    for (const line of (value.stack ?? '').split('\n').slice(1)) {
      const match = line.match(/^\s*at\s+(?:(.*?)\s+\()?(.+?):(\d+):\d+\)?$/);
      if (match) frames.push({ file: bounded(match[2]!,512), function: bounded(match[1] ?? '<anonymous>',512),
        line: Number(match[3]), in_app: !match[2]!.startsWith('node:') && !match[2]!.includes('/node_modules/') });
      if (frames.length === 32) break;
    }
    const event: FailureEvent = {
      event_id: randomUUID(), occurred_at: new Date().toISOString(), exception_type: bounded(value.name,256),
      message: bounded(value.message,2048), stack_trace: value.stack ? bounded(value.stack,16384) : undefined, frames,
      trace_id: context.trace_id, span_id: context.span_id, request_id: context.request_id,
      invocation_id: context.invocation_id, route: context.route, source_kind: context.source_kind,
    };
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
          headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${options.token}` }, body: JSON.stringify(event) });
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
  return { captureException, flush, close, install, wrap, stats: () => ({ queued: queue.length, accepted, dropped }) };
}
