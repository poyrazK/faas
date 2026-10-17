/** Internal closed vocabularies shared by collection and exposition. */
import type { EventAcknowledgmentPreparationEvent, EventAcknowledgmentPreparationOutcome,
  EventAcknowledgmentPreparationReason, EventAcknowledgmentPreparationStage } from './event-acknowledgment-preparation-events.js';

export const eventZeroes = Object.freeze({ started: 0, claim_acquired: 0, preparation_started: 0, preparation_finished: 0,
  renewal_failed: 0, intent_committed: 0, commit_unknown: 0, shutdown: 0 } satisfies Record<EventAcknowledgmentPreparationEvent['kind'], number>);
export const outcomeZeroes = Object.freeze({ committed: 0, commit_unknown: 0, cancelled: 0, failed: 0 } satisfies
  Record<EventAcknowledgmentPreparationOutcome, number>);
export const reasonZeroes = Object.freeze({ cancelled: 0, preparation_timeout: 0, claim_busy: 0, local_lease_lost: 0,
  local_operation_busy: 0, local_store_failed: 0, platform_lease_lost: 0, platform_unconfirmed: 0,
  receipt_conflict: 0, receipt_missing: 0, intent_conflict: 0, intent_superseded: 0, intent_store_failed: 0,
  commit_unknown: 0, failed: 0 } satisfies Record<EventAcknowledgmentPreparationReason, number>);
export const stageZeroes = Object.freeze({ receipt_reconciliation: 0, platform_start: 0, claim: 0, local_renewal: 0,
  preparation: 0, local_handoff: 0, completion: 0 } satisfies Record<EventAcknowledgmentPreparationStage, number>);
