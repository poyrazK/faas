/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Same-source obligation succession recorded atomically with a source change. Links require overlapping saved case coverage and removed/new cycles or explicit transition references; a retained terminal same-kind predecessor may link a later cycle. Generic new-cycle reasons describe succession rather than business causation. Reinstated known identities, unrelated cases and missing history are not linked. These edges do not prove fulfillment.
 */
export type OperationWorkflowObligationReplacement = {
  predecessor_id: string;
  successor_id: string;
  predecessor_kind: string;
  successor_kind: string;
  reason: string;
  reason_kind: 'recorded_decision' | 'recorded_reassignment' | 'revised_commitment' | 'new_cycle';
};

