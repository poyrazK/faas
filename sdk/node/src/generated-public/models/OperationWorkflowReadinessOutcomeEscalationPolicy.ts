/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in deadline escalation policy frozen at completion. Requires an outcome watch and future outcome deadline. Schedules one assigned follow-up on the deadline notification; never executes business actions.
 */
export type OperationWorkflowReadinessOutcomeEscalationPolicy = {
  /**
   * OperationWorkflowReadinessOutcomeEscalationPolicy.acknowledge_after_seconds. Optional positive response target from escalation delivery; zero or omission disables it. Bounded to one week.
   */
  acknowledge_after_seconds?: number;
  /**
   * OperationWorkflowReadinessOutcomeEscalationPolicy.fallback_owner. Optional different owner label requiring a response target. Applied once only to open unacknowledged escalation with unchanged original ownership and no assignment history.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowReadinessOutcomeEscalationPolicy.owner. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  owner: string;
  /**
   * OperationWorkflowReadinessOutcomeEscalationPolicy.note. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  note?: string;
  /**
   * OperationWorkflowReadinessOutcomeEscalationPolicy.follow_up_after_seconds. Retained coordination escalation policy or evidence; business interpretation and execution remain application-owned.
   */
  follow_up_after_seconds: number;
};

