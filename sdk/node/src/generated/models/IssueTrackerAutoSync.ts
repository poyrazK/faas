/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in tracker direction and latest durable worker outcome. Inbound resolutions require explicit fixing-deployment evidence.
 */
export type IssueTrackerAutoSync = {
  /**
   * Outbound pushes Gregale lifecycle; inbound applies verified reopenings; both enables both behaviors.
   */
  direction?: 'outbound' | 'inbound' | 'both';
  /**
   * Last provider lifecycle observed by the worker including changes held for review.
   */
  observed_state?: 'open' | 'resolved';
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

