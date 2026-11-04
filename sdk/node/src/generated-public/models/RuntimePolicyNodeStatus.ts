/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fresh app-level host policy status for compute nodes currently hosting live instances of this app. This shape is used by the egress_allowlist and cpu_limit components.
 */
export type RuntimePolicyNodeStatus = {
  scope?: 'app';
  desired_revision: number;
  state: 'active' | 'pending' | 'unverified';
  serving_nodes: number;
  applied_nodes: number;
  pending_nodes: number;
  stale_nodes: number;
};

