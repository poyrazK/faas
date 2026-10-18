/** Finite saved-callback recovery with bounded admissions and joined progress. */
import { inspect } from 'node:util';
import { EventConsumerCommitUnknownError, type EventConsumerPool } from './event-consumer-transactions.js';
import { intentPool, intentScope } from './event-acknowledgment-intent-store.js';
import type { EventAcknowledgmentIntentRecoveryOptions } from './event-acknowledgment-intent-recovery.js';
import { recoveryPassPolicy, runRecoveryPass } from './event-acknowledgment-intent-recovery-pass-runtime.js';

export interface EventAcknowledgmentIntentRecoveryPassOptions extends EventAcknowledgmentIntentRecoveryOptions {
  /** Admitted iterations, including empty probes/acquisition failures; integer 1..10000, default 100. */
  readonly maxAttempts?: number;
  /** Concurrent recovery iterations; integer 1..32, default four. */
  readonly concurrency?: number;
}
export interface EventAcknowledgmentIntentRecoveryPassResult {
  readonly stopReason: 'limit' | 'empty' | 'interrupted' | 'failed' | 'commit_unknown';
  readonly attempts: number;
  readonly confirmed: number;
  readonly unconfirmed: number;
  /** Known parked outcomes, including expired exhausted claims requiring no callback. */
  readonly parked: number;
  readonly superseded: number;
  readonly empty: number;
  readonly claimLost: number;
  readonly interrupted: number;
  readonly failed: number;
  /** Typed uncertainty can overlap a known returned outcome if pool release itself reports it. */
  readonly commitUnknown: number;
  /** Admissions with a failed synchronous pool release; can overlap known settlements. */
  readonly cleanupFailures: number;
}

export class EventAcknowledgmentIntentRecoveryPassError extends Error {
  readonly result: EventAcknowledgmentIntentRecoveryPassResult;
  constructor(result: EventAcknowledgmentIntentRecoveryPassResult,
    readonly code: 'event_acknowledgment_intent_recovery_pass_failed' | 'event_acknowledgment_intent_unsupported_generation'
      = 'event_acknowledgment_intent_recovery_pass_failed') {
    super(code === 'event_acknowledgment_intent_unsupported_generation'
      ? 'acknowledgment intent exceeds the Node safe-integer range; use a compatible recovery host'
      : 'acknowledgment intent recovery pass failed; inspect retained intents');
    this.name = 'EventAcknowledgmentIntentRecoveryPassError';
    this.result = Object.freeze({ ...result });
  }
  toJSON(): object { return { name: this.name, code: this.code, result: this.result }; }
  [inspect.custom](): object { return this.toJSON(); }
}

/** Keeps the existing uncertain-COMMIT instanceof/code without retaining its raw cause. */
export class EventAcknowledgmentIntentRecoveryPassCommitUnknownError extends EventConsumerCommitUnknownError {
  readonly result: EventAcknowledgmentIntentRecoveryPassResult;
  constructor(result: EventAcknowledgmentIntentRecoveryPassResult) {
    super(undefined);
    this.name = 'EventAcknowledgmentIntentRecoveryPassCommitUnknownError';
    this.message = 'acknowledgment intent recovery commit outcome unknown; reconcile on fresh connections';
    this.result = Object.freeze({ ...result });
  }
  toJSON(): object { return { name: this.name, code: this.code, result: this.result }; }
  [inspect.custom](): object { return this.toJSON(); }
}

/** Native AbortError with frozen progress; caller-provided abort reasons are not forwarded. */
export class EventAcknowledgmentIntentRecoveryPassCancelled extends DOMException {
  readonly result: EventAcknowledgmentIntentRecoveryPassResult;
  constructor(result: EventAcknowledgmentIntentRecoveryPassResult) {
    super('acknowledgment intent recovery pass interrupted', 'AbortError');
    this.result = Object.freeze({ ...result });
  }
  toJSON(): object { return { name: this.name, code: this.code, result: this.result }; }
  [inspect.custom](): object { return this.toJSON(); }
}

/** One explicit pass. Hosts schedule later passes and own the pool/client lifetime.
 * Empty probes stop admission without establishing a globally drained backlog. */
export async function recoverEventAcknowledgmentIntents(pool: EventConsumerPool, accountId: string, appId: string,
  options: EventAcknowledgmentIntentRecoveryPassOptions = {}): Promise<EventAcknowledgmentIntentRecoveryPassResult> {
  const policy = recoveryPassPolicy(options);
  intentPool(pool);
  const [account, app] = intentScope(accountId, appId);
  const { result, unsupportedGeneration } = await runRecoveryPass(pool, account, app, policy);
  if (result.stopReason === 'commit_unknown') throw new EventAcknowledgmentIntentRecoveryPassCommitUnknownError(result);
  if (result.stopReason === 'interrupted') throw new EventAcknowledgmentIntentRecoveryPassCancelled(result);
  if (result.stopReason === 'failed') throw new EventAcknowledgmentIntentRecoveryPassError(result,
    unsupportedGeneration ? 'event_acknowledgment_intent_unsupported_generation' : undefined);
  return result;
}
