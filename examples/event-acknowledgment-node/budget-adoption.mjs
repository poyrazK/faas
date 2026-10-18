import {
  adoptEventAcknowledgmentIntentRetryBudget,
  getEventAcknowledgmentIntentBudgetAdoptionCandidate,
} from '@gregale/sdk-node';

// Select precise metadata for an explicit authorized operator decision.
// This acquires no claim/row lock and changes no intent or command.
export async function selectBudgetAdoption(pool, scope, key, commandId, maxDeliveryAttempts, signal) {
  const selected = await getEventAcknowledgmentIntentBudgetAdoptionCandidate(pool,
    scope.accountId, scope.appId, key.invocationId, key.replayGeneration, key.attempt, { signal });
  if (!['pending', 'unconfirmed'].includes(selected.state) || selected.retryPolicy?.version === 2) {
    throw new Error('select an active unbound or v1 intent');
  }
  const prior = selected.retryPolicy;
  const expectedPolicy = prior === undefined ? undefined : Object.freeze({ ...prior });
  const retryPolicy = prior === undefined ? undefined : Object.freeze({
    initialBackoffMs: prior.initialBackoffMs, maxBackoffMs: prior.maxBackoffMs,
  });
  return Object.freeze({ invocationId: selected.invocationId, replayGeneration: selected.replayGeneration, attempt: selected.attempt,
    options: Object.freeze({ commandId, expectedUpdatedAt: selected.updatedAt, expectedDeliveryAttempts: selected.deliveryAttempts,
      expectedPolicy, retryBudget: Object.freeze({ maxDeliveryAttempts, retryPolicy }) }) });
}

// Durably persist the command above AND its trusted owner scope before calling.
// After uncertainty, retry this original command; never refresh a fence under its ID.
export async function applyBudgetAdoption(pool, scope, savedCommand, signal) {
  return adoptEventAcknowledgmentIntentRetryBudget(pool, scope.accountId, scope.appId,
    savedCommand.invocationId, savedCommand.replayGeneration, savedCommand.attempt, { ...savedCommand.options, signal });
}
