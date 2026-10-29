import {
  adoptEventAcknowledgmentIntentRetryPolicy,
  getEventAcknowledgmentIntentPolicyAdoptionCandidate,
} from '@gregale/sdk-node/preview';

// Read metadata for a deliberate operator decision; this grants no claim.
export async function selectPolicyAdoption(pool, scope, key, commandId, signal) {
  const selected = await getEventAcknowledgmentIntentPolicyAdoptionCandidate(pool,
    scope.accountId, scope.appId, key.invocationId, key.replayGeneration, key.attempt, { signal });
  return Object.freeze({ invocationId: selected.invocationId,
    replayGeneration: selected.replayGeneration, attempt: selected.attempt,
    options: Object.freeze({ commandId, expectedUpdatedAt: selected.updatedAt,
      expectedDeliveryAttempts: selected.deliveryAttempts,
      retryPolicy: Object.freeze({ initialBackoffMs: 1000, maxBackoffMs: 300000 }) }) });
}

// Durably save the command above under the trusted owner scope before calling.
// Resolve every uncertain result with this original command; never refresh its fence.
export async function applyPolicyAdoption(pool, scope, savedCommand, signal) {
  return adoptEventAcknowledgmentIntentRetryPolicy(pool, scope.accountId, scope.appId,
    savedCommand.invocationId, savedCommand.replayGeneration, savedCommand.attempt,
    { ...savedCommand.options, signal });
}
