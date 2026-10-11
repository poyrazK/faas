/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in deadline escalation policy frozen at completion. Requires an outcome watch and future outcome deadline. Schedules one assigned follow-up on the deadline notification; never executes business actions.
 */
export type OperationWorkflowReadinessOutcomeEscalationPolicy = {
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

