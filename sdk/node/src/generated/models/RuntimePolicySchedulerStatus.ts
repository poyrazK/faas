/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fresh observation of the desired scaling policy by the owning schedd. Active means the policy was loaded, not that a metric-driven replica target has been reached.
 */
export type RuntimePolicySchedulerStatus = {
  scope: 'app';
  desired_revision: number;
  observed_revision: number;
  state: 'active' | 'pending' | 'unverified';
  /**
   * Scheduler owner node when this app is sharded.
   */
  scheduler_node_id?: string;
  observed_at?: string | null;
  stale: boolean;
};

