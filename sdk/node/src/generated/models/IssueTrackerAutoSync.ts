/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in automatic Gregale-to-GitHub or Linear policy and latest durable worker outcome for a tracker reference.
 */
export type IssueTrackerAutoSync = {
  link_id: string;
  enabled: boolean;
  revision: number;
  source_issue_id: string;
  source_state?: 'open' | 'resolved';
  provider_state?: 'open' | 'resolved';
  pending_state?: 'open' | 'resolved';
  outcome: 'disabled' | 'matched' | 'pending' | 'applied' | 'error' | 'conflict' | 'blocked';
  reason?: string;
  last_checked_at?: string;
  next_check_at?: string;
  /**
   * Selected open workflow destination for Linear policies.
   */
  linear_open_state_id?: string;
  /**
   * Selected resolved workflow destination for Linear policies.
   */
  linear_resolved_state_id?: string;
};

