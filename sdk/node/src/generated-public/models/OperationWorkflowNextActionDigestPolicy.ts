/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fixed team digest selection and cadence. Owner is both the exact action-owner filter and inbox recipient label, not proof of team membership. Overdue actions, deadlines within the configured approaching window and unaccepted handoffs qualify. Unchanged delivered items are suppressed.
 */
export type OperationWorkflowNextActionDigestPolicy = {
  /**
   * Nonblank trimmed control-free UTF-8 team label, at most 256 bytes.
   */
  owner: string;
  /**
   * Optional exact workflow selector, independent of the subscription discovery workflow.
   */
  workflow?: string;
  /**
   * Optional exact next-action kind supported by the work queue.
   */
  action_kind?: string;
  action_trigger?: 'human' | 'automatic' | 'suggested';
  /**
   * Digest cadence, default one hour. The first evaluation is scheduled from subscription creation; retries retain the due time.
   */
  cadence_seconds?: number;
  /**
   * Approaching deadline window from each successful evaluation, default one day. Undated suggestions do not qualify.
   */
  soon_after_seconds?: number;
};

