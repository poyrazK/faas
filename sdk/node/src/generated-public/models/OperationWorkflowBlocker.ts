/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public application-reported reason a named target Operation must wait. Reports replace the entire prior blocker list at their state revision; these observations do not grant or enforce execution authority.
 */
export type OperationWorkflowBlocker = {
  /**
   * Application-assigned urgency. Omitted means normal for queue filtering and counts.
   */
  priority?: 'low' | 'normal' | 'high' | 'urgent';
  /**
   * Public application-reported impact limited to 512 UTF-8 bytes without control characters.
   */
  business_impact?: string;
  /**
   * Application acknowledgement time paired with acknowledged_by. Must be at or after known first observation and at or before report time.
   */
  acknowledged_at?: string;
  /**
   * Public application actor identifier paired with acknowledged_at. Limited to 128 UTF-8 bytes without control characters.
   */
  acknowledged_by?: string;
  /**
   * Optional follow-up deadline at or after acknowledgement. Does not resolve the blocker or reset its age.
   */
  follow_up_at?: string;
  /**
   * Optional public application-assigned person or team identifier, limited to 128 UTF-8 bytes without control characters. Empty or omitted means unassigned; this grants no execution authority.
   */
  owner?: string;
  /**
   * Optional public application-suggested resolution step, limited to 512 UTF-8 bytes without control characters. This is guidance rather than an executable command.
   */
  next_action?: string;
  /**
   * Optional application observation time at or before the containing report. Upgraded transactional SDKs preserve it across repeats of the same target/code until cleared. Omitted means unknown.
   */
  first_observed_at?: string;
  code: string;
  /**
   * Public UTF-8 text limited to 512 bytes without control characters.
   */
  description: string;
  /**
   * Target Operation name.
   */
  operation: string;
};

