import { bindService, type ClientConfig } from './client-instance.js';
import type { DeploymentsService } from './generated/services/DeploymentsService.js';
import type { BuildResponse } from './generated/models/BuildResponse.js';
import type { ReleaseTimeline } from './generated/models/ReleaseTimeline.js';
import type { RequestOptions } from './request-options.js';

export type WaiterSnapshot<T> = T extends readonly (infer Item)[] ? readonly WaiterSnapshot<Item>[]
  : T extends object ? { readonly [Key in keyof T]: WaiterSnapshot<T[Key]> } : T;

export interface WaiterProgress {
  readonly poll: number;
  readonly elapsedMs: number;
}

export interface WaiterOptions<T> extends Omit<RequestOptions, 'timeoutMs'> {
  /** Required total budget, including reads, retry waits and progress callbacks.
   * Positive integer milliseconds, at most 2147483647. */
  readonly timeoutMs: number;
  /** Positive integer milliseconds. Defaults to 1000. */
  readonly minDelayMs?: number;
  /** Positive integer milliseconds, at least minDelayMs. Defaults to 5000. */
  readonly maxDelayMs?: number;
  /** Maximum status reads, including the initial read. HTTP retries stay within a poll. */
  readonly maxPolls?: number;
  /** Awaited for each valid observation, including terminal states. Exceptions
   * stop waiting. Snapshots are deeply frozen and separate from the result. */
  readonly onProgress?: (value: WaiterSnapshot<T>, progress: WaiterProgress) => void | Promise<void>;
}

export type BuildWaitOptions = WaiterOptions<BuildResponse>;
export interface DeploymentWaitOptions extends WaiterOptions<ReleaseTimeline> {
  /** Defaults to true: approval waits and holds return a blocked error.
   * Set false to continue observing within the same total budget. */
  readonly stopOnBlocked?: boolean;
}

export type WaiterFailureCode = 'failed' | 'cancelled' | 'rolled_back' | 'superseded' | 'blocked'
  | 'not_serving' | 'regressed' | 'invalid_response' | 'unexpected_state' | 'timeout' | 'poll_limit';

/** Terminal/resource or budget failure. HTTP/callback failures and caller abort
 * retain their original identity. Invalid observations never replace the last valid one. */
export class WaiterError<T = BuildResponse | ReleaseTimeline> extends Error {
  override readonly name: string;
  constructor(
    readonly code: WaiterFailureCode, readonly resource: 'build' | 'deployment',
    readonly id: string, readonly polls: number, readonly lastObserved?: WaiterSnapshot<T>,
  ) {
    super(`SDK ${resource} wait stopped: ${code}`);
    this.name = code === 'timeout' ? 'TimeoutError' : 'WaiterError';
  }
}

type Service = Pick<typeof DeploymentsService, 'getBuild' | 'getReleaseTimeline'>;
export interface DeploymentWaiters {
  waitForBuild(params: Parameters<Service['getBuild']>[0], options: BuildWaitOptions): Promise<BuildResponse>;
  /** Wait for released/live with positive traffic and no recorded watch regression.
   * This observes the selected release; it is not a general application health check. */
  waitForDeployment(params: Parameters<Service['getReleaseTimeline']>[0], options: DeploymentWaitOptions): Promise<ReleaseTimeline>;
}

export function snapshot<T>(value: T): WaiterSnapshot<T> {
  const copy = structuredClone(value);
  const pending: object[] = copy && typeof copy === 'object' ? [copy] : [];
  const seen = new Set<object>();
  while (pending.length) {
    const item = pending.pop()!;
    if (seen.has(item)) continue;
    seen.add(item); Object.freeze(item);
    for (const child of Object.values(item)) if (child && typeof child === 'object') pending.push(child);
  }
  return copy as WaiterSnapshot<T>;
}

type Decision = 'success' | 'retry' | WaiterFailureCode;
function buildState(value: BuildResponse, id: string): Decision {
  if (!value || value.id !== id || typeof value.status !== 'string') return 'invalid_response';
  switch (value.status) {
    case 'queued': case 'running': return 'retry';
    case 'succeeded': return 'success';
    case 'failed': case 'cancelled': return value.status;
    default: return 'unexpected_state';
  }
}

function deploymentState(value: ReleaseTimeline, id: string, stopOnBlocked: boolean): Decision {
  if (!value || value.deployment_id !== id || typeof value.phase !== 'string' ||
      typeof value.status !== 'string' || !Number.isFinite(value.traffic_percent) || value.traffic_percent < 0 || value.traffic_percent > 100 ||
      !Array.isArray(value.events) || value.events.some(event => !event || typeof event.kind !== 'string')) return 'invalid_response';
  switch (value.phase) {
    case 'building': case 'scheduled': case 'rolling_out': case 'watching': return 'retry';
    case 'awaiting_approval': case 'held': return stopOnBlocked ? 'blocked' : 'retry';
    case 'failed': case 'cancelled': case 'rolled_back': case 'superseded': return value.phase;
    case 'released':
      if (value.events.some(event => event.kind === 'watch_regressed')) return 'regressed';
      return value.status === 'live' && value.traffic_percent > 0 ? 'success' : 'not_serving';
    default: return 'unexpected_state';
  }
}

async function wait<T>(
  read: (options: RequestOptions) => PromiseLike<T>, resource: 'build' | 'deployment', id: string,
  options: WaiterOptions<T>, decide: (value: T) => Decision,
): Promise<T> {
  const policy = { ...options, retry: options?.retry && { ...options.retry } };
  const min = policy.minDelayMs ?? 1000, max = policy.maxDelayMs ?? 5000;
  const validTimer = (value: number) => Number.isInteger(value) && value > 0 && value <= 2 ** 31 - 1;
  if (typeof id !== 'string' || !id) throw new TypeError('A resource id is required');
  if (!validTimer(policy.timeoutMs)) throw new TypeError('timeoutMs must be an integer from 1 through 2147483647');
  if (!validTimer(min) || !validTimer(max) || min > max) throw new TypeError('Delays must be integers from 1 through 2147483647, with minDelayMs <= maxDelayMs');
  if (policy.maxPolls !== undefined && (!Number.isSafeInteger(policy.maxPolls) || policy.maxPolls < 1)) throw new TypeError('maxPolls must be a positive safe integer');
  const controller = new AbortController(), caller = policy.signal;
  let polls = 0, lastObserved: WaiterSnapshot<T> | undefined;
  const failure = (code: WaiterFailureCode) => new WaiterError<T>(code, resource, id, polls, lastObserved);
  const start = performance.now(), deadline = start + policy.timeoutMs;
  const check = () => {
    controller.signal.throwIfAborted();
    if (performance.now() >= deadline) { controller.abort(failure('timeout')); controller.signal.throwIfAborted(); }
  };
  const onCallerAbort = () => controller.abort(caller?.reason);
  let rejectAbort!: (reason: unknown) => void;
  const aborted = new Promise<never>((_resolve, reject) => { rejectAbort = reject; });
  const onAbort = () => rejectAbort(controller.signal.reason);
  controller.signal.addEventListener('abort', onAbort, { once: true });
  caller?.addEventListener('abort', onCallerAbort, { once: true });
  const timer = setTimeout(() => controller.abort(failure('timeout')), policy.timeoutMs);
  async function run(): Promise<T> {
    if (caller?.aborted) onCallerAbort();
    while (true) {
      check(); polls++;
      // The waiter owns the total deadline, including asynchronous callbacks.
      // Child scopes retain fetch/body cancellation without a second timeout.
      const value = await read({ signal: controller.signal, timeoutMs: 0, retry: policy.retry, onResponse: policy.onResponse });
      check();
      const decision = decide(value);
      if (decision === 'invalid_response' || decision === 'unexpected_state') throw failure(decision);
      lastObserved = snapshot(value);
      check();
      await policy.onProgress?.(lastObserved, Object.freeze({ poll: polls, elapsedMs: Math.max(0, performance.now() - start) }));
      check();
      if (decision === 'success') return value;
      if (decision !== 'retry') throw failure(decision);
      if (polls >= (policy.maxPolls ?? Infinity)) throw failure('poll_limit');
      const ceiling = Math.min(max, min * 2 ** Math.min(31, polls - 1));
      const delay = Math.min(deadline - performance.now(), min + Math.floor(Math.random() * (ceiling - min + 1)));
      await new Promise<void>((resolve, reject) => {
        const done = () => { clearTimeout(waitTimer); controller.signal.removeEventListener('abort', stop); };
        const stop = () => { done(); reject(controller.signal.reason); };
        const waitTimer = setTimeout(() => { done(); resolve(); }, Math.max(1, Math.ceil(delay)));
        controller.signal.addEventListener('abort', stop, { once: true });
      });
    }
  }
  try {
    return await Promise.race([aborted, run()]);
  } finally {
    clearTimeout(timer);
    caller?.removeEventListener('abort', onCallerAbort);
    controller.signal.removeEventListener('abort', onAbort);
  }
}

/** Capture one immutable client configuration and copied options per invocation. */
export function bindDeploymentWaiters(service: Service, getConfig: () => ClientConfig): DeploymentWaiters {
  return {
    waitForBuild({ id }, options) {
      const config = getConfig(), requests = bindService(service, () => config);
      return wait(requestOptions => requests.getBuild({ id }, requestOptions), 'build', id, options, value => buildState(value, id));
    },
    waitForDeployment({ id }, options) {
      const config = getConfig(), requests = bindService(service, () => config);
      if (options?.stopOnBlocked !== undefined && typeof options.stopOnBlocked !== 'boolean') return Promise.reject(new TypeError('stopOnBlocked must be a boolean'));
      const stopOnBlocked = options?.stopOnBlocked ?? true;
      return wait(requestOptions => requests.getReleaseTimeline({ id }, requestOptions), 'deployment', id, options, value => deploymentState(value, id, stopOnBlocked));
    },
  };
}
