import { jobScope } from './job-runtime.js';
import { parseLiveJobSnapshot, type LiveJobSnapshot } from './live-jobs.js';
import type { TypedPipelineRun } from './job-pipeline-client.js';
import type { JobProgress } from './typed-jobs.js';
import { PipelineError } from './pipeline-state.js';

/** Application snapshot adapter for the existing live view/bridge. Authorize
 * this workflow's owner before loading. Native workflow status is authoritative;
 * a completed Job alone cannot establish successful pipeline completion.
 */
export function jobPipelineLiveSnapshot<O>(run: TypedPipelineRun<O>, options: {
  resourceId: string; steps: readonly string[]; timeoutMs: number; signal?: AbortSignal;
  inspectJob(step: string, signal: AbortSignal): Promise<{ progress: JobProgress | null }>;
  /** Materialize/verify the owned report before returning true. */
  reportReady?(output: O, signal: AbortSignal): Promise<boolean>;
}): Promise<LiveJobSnapshot> {
  const deadline = performance.now() + options.timeoutMs;
  return jobScope(options.timeoutMs, options.signal, async signal => {
    const check = () => { signal.throwIfAborted(); if (performance.now() >= deadline) throw new PipelineError('deadline'); };
    const workflow = await run.inspect({ timeoutMs: options.timeoutMs, signal }); check();
    const states: (JobProgress | null)[] = [];
    for (const step of options.steps) { states.push((await options.inspectJob(step, signal)).progress); check(); }
    const count = (status: string) => states.filter(s => s?.status === status).length;
    const status = workflow.cancelled ? 'cancelled' : workflow.status === 'dead' ? 'dead_letter' : ['failed', 'succeeded'].includes(workflow.status) ? workflow.status : workflow.status === 'pending' ? 'queued' : 'running';
    let report: LiveJobSnapshot['report'] = 'none';
    if (status === 'succeeded' && options.reportReady) {
      const output = await run.result({ timeoutMs: options.timeoutMs, signal }); check();
      report = await options.reportReady(output, signal) ? 'ready' : 'pending'; check();
    }
    return parseLiveJobSnapshot({ version: 1, resource_id: options.resourceId, run_id: run.checkpoint.run_id, status,
      total: states.length, succeeded: count('succeeded'), failed: count('failed') + count('dead_letter'), cancelled: count('cancelled'), running: count('running'), report,
      failures: options.steps.flatMap((step, index) => ['failed', 'dead_letter'].includes(states[index]?.status ?? '') ? [{ input_id: step, code: 'job_failed' }] : []),
    }, options.resourceId);
  });
}
