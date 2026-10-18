import {
  getEventAcknowledgmentIntentRetryResumeCandidate,
  resumeEventAcknowledgmentIntentRetryBudget,
} from '@gregale/sdk-node';

// Authenticate delivery and retain exclusive ingestion-job ownership first.
// applyBusinessSQL uses only the supplied transaction and returns exact JSON text.
export async function stageWithRetryBudget(worker, pool, applyBusinessSQL, signal) {
  return worker.runDurable(pool, applyBusinessSQL, { signal,
    retryBudget: { maxDeliveryAttempts: 8, retryPolicy: { initialBackoffMs: 1000, maxBackoffMs: 300000 } } });
}

// Select exact metadata for a deliberate, authorized operator decision.
// This function grants no claim and sends no callback.
export async function selectRetryResume(pool, scope, key, commandId, newDeliveryAttemptLimit, signal) {
  const selected = await getEventAcknowledgmentIntentRetryResumeCandidate(pool,
    scope.accountId, scope.appId, key.invocationId, key.replayGeneration, key.attempt, { signal });
  if (selected.state !== 'parked' || selected.retryPolicy?.version !== 2) throw new Error('select a parked budgeted intent');
  return Object.freeze({ invocationId: selected.invocationId, replayGeneration: selected.replayGeneration, attempt: selected.attempt,
    options: Object.freeze({ commandId, expectedUpdatedAt: selected.updatedAt, expectedDeliveryAttempts: selected.deliveryAttempts,
      expectedDeliveryAttemptLimit: selected.deliveryAttemptLimit, newDeliveryAttemptLimit }) });
}

// Durably retain the command above in the trusted owner scope BEFORE calling.
// Retry the exact original command after uncertainty; never refresh its fences.
export async function applyRetryResume(pool, scope, savedCommand, signal) {
  return resumeEventAcknowledgmentIntentRetryBudget(pool, scope.accountId, scope.appId,
    savedCommand.invocationId, savedCommand.replayGeneration, savedCommand.attempt, { ...savedCommand.options, signal });
}
