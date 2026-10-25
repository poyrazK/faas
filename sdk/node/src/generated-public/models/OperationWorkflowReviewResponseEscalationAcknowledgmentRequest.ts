/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One immutable coordination commitment for the current escalated, unaccepted handoff. Preserve the parent response owner, plan and exact check-in, even if overdue. Cannot combine with a decision, acceptance, decline or reassignment. Current handoff and escalation timestamps are required. Due date must be in the future and within seven days.
 */
export type OperationWorkflowReviewResponseEscalationAcknowledgmentRequest = {
  decision_at: string;
  required_at: string;
  handed_off_at: string;
  escalated_at: string;
  /**
   * Control-free trimmed UTF-8 coordinator or team label, nonblank and limited to 256 bytes. Uses the existing authorized operator model; not proof of team membership.
   */
  coordinator: string;
  /**
   * Nonblank UTF-8 next-action plan, limited to 1024 bytes; line breaks and tabs are allowed.
   */
  plan: string;
  /**
   * Future next-action deadline within seven days. This does not extend the handoff acceptance or decision-review deadlines.
   */
  due_at: string;
};

