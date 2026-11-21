import { RunsService } from './generated/services/RunsService.js';
import type { ExecutionEnvironmentEvent } from './generated/models/ExecutionEnvironmentEvent.js';

export type EnvironmentDiagnostic =
  | { type: 'diagnostic'; event: ExecutionEnvironmentEvent }
  | { type: 'gap'; after: number; through_sequence: number }
  | { type: 'terminal'; status: string; next_sequence: number };
export interface WatchEnvironmentOptions {
  after?: number;
  signal?: AbortSignal;
  pollIntervalMs?: number;
}
function pause(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const finish = () => { clearTimeout(timer); signal?.removeEventListener('abort', abort); resolve(); };
    const abort = () => { clearTimeout(timer); signal?.removeEventListener('abort', abort); reject(signal?.reason ?? new Error('Environment watch aborted')); };
    const timer = setTimeout(finish, ms);
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) abort();
  });
}
/** Resume durable build diagnostics; closing a watcher never cancels a build. */
export async function* watchExecutionEnvironment(
  id: string, options: WatchEnvironmentOptions = {},
): AsyncGenerator<EnvironmentDiagnostic> {
  let after = options.after ?? 0;
  const interval = options.pollIntervalMs ?? 2000;
  if (!Number.isSafeInteger(after) || after < 0 || !Number.isFinite(interval) || interval <= 0) throw new Error('Invalid environment watch options');
  for (;;) {
    options.signal?.throwIfAborted();
    const pending = RunsService.getExecutionEnvironmentEvents({ id, after });
    const abort = () => pending.cancel();
    options.signal?.addEventListener('abort', abort, { once: true });
    if (options.signal?.aborted) abort();
    let page;
    try { page = await pending; } finally { options.signal?.removeEventListener('abort', abort); }
    if (page.next_sequence < after) throw new Error('Environment diagnostic cursor regressed');
    if (page.gap) yield { type: 'gap', after, through_sequence: (page.events[0]?.sequence ?? after + 1) - 1 };
    for (const event of page.events) yield { type: 'diagnostic', event };
    after = page.next_sequence;
    if (['ready', 'failed', 'retiring', 'deleted'].includes(page.status)) {
      yield { type: 'terminal', status: page.status, next_sequence: after };
      return;
    }
    await pause(interval, options.signal);
  }
}
