/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlockerPerformance } from './OperationWorkflowBlockerPerformance.js';
import type { OperationWorkflowDurationDistribution } from './OperationWorkflowDurationDistribution.js';
import type { OperationWorkflowPerformanceCoverageReason } from './OperationWorkflowPerformanceCoverageReason.js';
import type { OperationWorkflowStatePerformance } from './OperationWorkflowStatePerformance.js';
import type { OperationWorkflowVerificationPerformance } from './OperationWorkflowVerificationPerformance.js';
/**
 * Counts distinguish all matches from the latest retained sample and complete eligible histories. Every duration excludes incomplete histories. Groups are ranked by total duration descending with stable identity ties. Overall distributions include eligible zero-duration instances; group distributions include only instances that reported that group. Overlapping blockers and verification waits are counted separately in their groups.
 */
export type OperationWorkflowPerformanceCohort = {
  sla_configured_workflow_count: number;
  sla_evaluated_workflow_count: number;
  sla_breached_workflow_count: number;
  sla_unknown_workflow_count: number;
  matching_workflow_count: number;
  sampled_workflow_count: number;
  complete_history_workflow_count: number;
  excluded_incomplete_workflow_count: number;
  cohort_truncated: boolean;
  exclusions: Array<OperationWorkflowPerformanceCoverageReason>;
  state_time: OperationWorkflowDurationDistribution;
  blocked_time: OperationWorkflowDurationDistribution;
  verification_wait: OperationWorkflowDurationDistribution;
  states: Array<OperationWorkflowStatePerformance>;
  blockers: Array<OperationWorkflowBlockerPerformance>;
  verification_owners: Array<OperationWorkflowVerificationPerformance>;
  states_truncated: boolean;
  blockers_truncated: boolean;
  verification_owners_truncated: boolean;
};

