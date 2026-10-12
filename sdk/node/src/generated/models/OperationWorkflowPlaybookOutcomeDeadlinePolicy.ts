/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookOutcomeDeadlinePolicy. Application-defined business attention policy. Requires an expected terminal outcome; all durations are whole seconds and the owner is a coordination label, not an external destination.
 */
export type OperationWorkflowPlaybookOutcomeDeadlinePolicy = {
  /**
   * OperationWorkflowPlaybookOutcomeDeadlinePolicy.after_seconds. Expected wait from checklist creation, bounded to ten years.
   */
  after_seconds: number;
  /**
   * OperationWorkflowPlaybookOutcomeDeadlinePolicy.owner. Required responsible team label with no surrounding whitespace or control characters.
   */
  owner: string;
  /**
   * OperationWorkflowPlaybookOutcomeDeadlinePolicy.follow_up_after_seconds. Assigned follow-up delay from actual notice delivery, independently bounded to one week.
   */
  follow_up_after_seconds: number;
  /**
   * OperationWorkflowPlaybookOutcomeDeadlinePolicy.note. Optional initial plain-text follow-up note; subsequent human notes are preserved.
   */
  note?: string;
};

