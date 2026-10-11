import { OperationHTTPError } from './customer-operations.js';
import type { OperationExecutionControlResponse } from './generated/models/OperationExecutionControlResponse.js';
import type { OperationWorkflowControlResponse } from './generated/models/OperationWorkflowControlResponse.js';

export type OperationStopCode = 'cancellation_requested' | 'deadline_exceeded' | 'execution_lease_expired' | 'execution_authority_lost' | 'control_unavailable' | 'request_finished';

/** Stopping is local cooperation, not evidence that external effects were undone. */
export class OperationStoppedError extends Error {
  constructor(readonly code: OperationStopCode) {
    super(`Operation handler stopped (${code})`);
    this.name = 'OperationStoppedError';
  }
}

export interface OperationHandlerScope {
  readonly signal: AbortSignal;
  readonly deadlineAt: string;
  /** Refresh the current claim before starting another unit of work. */
  checkpoint(): Promise<void>;
  /** Check the last observed time budget, including during CPU work. */
  throwIfStopped(): void;
}

export function validateOperationControl(value: OperationExecutionControlResponse, execution: { id: string; invocationID: string; attempt: number }): OperationExecutionControlResponse {
  if (!value || value.operation_id !== execution.id || value.invocation_id !== execution.invocationID || value.attempt !== execution.attempt ||
      !validControlObservation(value)) {
    throw new OperationStoppedError('execution_authority_lost');
  }
  // Copy only the documented observation; no authority or response extras escape.
  return Object.freeze({ operation_id: value.operation_id, invocation_id: value.invocation_id, attempt: value.attempt,
    cancellation_requested: value.cancellation_requested, deadline_at: value.deadline_at, lease_expires_at: value.lease_expires_at,
    observed_at: value.observed_at, poll_after_ms: value.poll_after_ms });
}

// Share time-budget mechanics only; each wire validator checks its execution family.
type ControlObservation = Pick<OperationExecutionControlResponse, 'cancellation_requested' | 'deadline_at' | 'lease_expires_at' | 'observed_at' | 'poll_after_ms'>;
function validControlObservation(value: ControlObservation): boolean {
  const date = (text: unknown): number => typeof text === 'string' && /^\d{4}-\d{2}-\d{2}T.*(?:Z|[+-]\d{2}:\d{2})$/.test(text) ? Date.parse(text) : NaN;
  const observed = date(value.observed_at), deadline = date(value.deadline_at), lease = date(value.lease_expires_at);
  return typeof value.cancellation_requested === 'boolean' && Number.isInteger(value.poll_after_ms) && value.poll_after_ms >= 100 && value.poll_after_ms <= 1000 && Number.isFinite(observed) && Number.isFinite(deadline) && Number.isFinite(lease) && lease <= deadline;
}
export function validateWorkflowOperationControl(value: OperationWorkflowControlResponse, execution: {id: string; runID: string; step: string; generation: number; attempt: number}): OperationWorkflowControlResponse {
  if (!value || value.operation_id !== execution.id || value.workflow_run_id !== execution.runID || value.workflow_step !== execution.step || value.generation !== execution.generation || value.attempt !== execution.attempt || !validControlObservation(value)) throw new OperationStoppedError('execution_authority_lost');
  return Object.freeze({ operation_id: value.operation_id, workflow_run_id: value.workflow_run_id, workflow_step: value.workflow_step,
    generation: value.generation, attempt: value.attempt, cancellation_requested: value.cancellation_requested, deadline_at: value.deadline_at,
    lease_expires_at: value.lease_expires_at, observed_at: value.observed_at, poll_after_ms: value.poll_after_ms });
}

const clock = (): { monotonic: number; wall: number } => ({ monotonic: performance.now(), wall: Date.now() });
const elapsed = (start: ReturnType<typeof clock>, now = clock()): number => Math.max(0, now.monotonic - start.monotonic, now.wall - start.wall);

/** Private request lifetime. Polls never overlap, retry work or renew the lease. */
export class OperationControlScope {
  readonly controller = new AbortController();
  private observation?: ControlObservation;
  private deadlineBudget?: { start: ReturnType<typeof clock>; duration: number };
  private leaseBudget?: { start: ReturnType<typeof clock>; duration: number };
  private budget?: { start: ReturnType<typeof clock>; duration: number; code: OperationStopCode };
  private expiry?: ReturnType<typeof setTimeout>;
  private pending?: Promise<void>;
  private polling?: Promise<void>;

  constructor(private readonly read: (signal: AbortSignal) => Promise<ControlObservation>, private readonly guard: () => void) {}

  throwIfStopped(): void {
    this.guard();
    if (this.budget && elapsed(this.budget.start) >= this.budget.duration) this.stop(this.budget.code);
    this.controller.signal.throwIfAborted();
  }

  checkpoint(): Promise<void> {
    this.throwIfStopped();
    if (this.pending) return this.pending;
    const pending = Promise.resolve().then(async () => {
      const start = clock();
      try {
        const value = await this.read(this.controller.signal);
        this.throwIfStopped();
        const prior = this.observation;
        if (prior && (value.deadline_at !== prior.deadline_at || Date.parse(value.observed_at) < Date.parse(prior.observed_at))) throw new OperationStoppedError('execution_authority_lost');
        const observed = Date.parse(value.observed_at), deadline = Date.parse(value.deadline_at), lease = Date.parse(value.lease_expires_at);
        // Reading the same lease is not renewal. Preserve its original local
        // expiry, and never refresh the admitted deadline's initial budget.
        this.deadlineBudget ??= { start, duration: deadline - observed };
        if (!prior || lease > Date.parse(prior.lease_expires_at)) this.leaseBudget = { start, duration: lease - observed };
        const candidate = { start, duration: lease - observed };
        const now = clock();
        if (candidate.duration - elapsed(candidate.start, now) < this.leaseBudget!.duration - elapsed(this.leaseBudget!.start, now)) this.leaseBudget = candidate;
        const deadlineFirst = this.deadlineBudget.duration - elapsed(this.deadlineBudget.start, now) <= this.leaseBudget!.duration - elapsed(this.leaseBudget!.start, now);
        this.budget = { ...(deadlineFirst ? this.deadlineBudget : this.leaseBudget!), code: deadlineFirst ? 'deadline_exceeded' : 'execution_lease_expired' };
        this.observation = value;
        if (value.cancellation_requested) this.stop('cancellation_requested');
        this.throwIfStopped();
        this.armExpiry();
      } catch (error) {
        if (!this.controller.signal.aborted) {
          const code = error instanceof OperationStoppedError ? error.code : error instanceof OperationHTTPError && [401, 404, 409, 410].includes(error.status) ? 'execution_authority_lost' : 'control_unavailable';
          this.stop(code);
        }
        this.controller.signal.throwIfAborted();
      }
    }).finally(() => { if (this.pending === pending) this.pending = undefined; });
    this.pending = pending;
    return pending;
  }

  async run<T>(handler: (scope: OperationHandlerScope) => T | Promise<T>): Promise<T> {
    try {
      await this.checkpoint(); // Do not enter business code without a live observation.
      const scope: OperationHandlerScope = Object.freeze({ signal: this.controller.signal, deadlineAt: this.observation!.deadline_at,
        checkpoint: () => this.checkpoint(), throwIfStopped: () => this.throwIfStopped() });
      this.polling = this.poll();
      const value = await handler(scope);
      await this.checkpoint(); // A stopped handler cannot return a successful result.
      return value;
    } catch (error) {
      // Signal-aware libraries may wrap our stop reason in AbortError. Keep
      // the public scope result consistent for handler and polling failures.
      this.controller.signal.throwIfAborted();
      throw error;
    } finally {
      this.stop('request_finished');
      if (this.expiry) clearTimeout(this.expiry);
      await this.polling;
    }
  }

  private stop(code: OperationStopCode): void {
    if (!this.controller.signal.aborted) this.controller.abort(new OperationStoppedError(code));
  }

  private armExpiry(): void {
    if (this.expiry) clearTimeout(this.expiry);
    const remaining = this.budget!.duration - elapsed(this.budget!.start);
    this.expiry = setTimeout(() => {
      try { this.throwIfStopped(); this.armExpiry(); } catch { /* stop has already aborted pending I/O */ }
    }, Math.max(1, Math.min(remaining, 2_147_483_647)));
  }

  private async poll(): Promise<void> {
    try {
      while (!this.controller.signal.aborted) {
        // The server recommends earlier reads for short scheduler claims and
        // bounds their frequency. The independent budget timer still expires.
        await delay(this.observation!.poll_after_ms, this.controller.signal);
        await this.checkpoint();
      }
    } catch { /* checkpoint or request cleanup supplies the stop reason */ }
  }
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const abort = (): void => { clearTimeout(timer); signal.removeEventListener('abort', abort); reject(signal.reason); };
    const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve(); }, ms);
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
  });
}
