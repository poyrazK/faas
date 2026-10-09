/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowInstanceMilestoneRef } from './OperationWorkflowInstanceMilestoneRef.js';
/**
 * One declared step with both current-page facts and a retention-wide summary for the selected contract version. Retention fields include all matching facts still retained under the normal Operation retention rules.
 */
export type OperationWorkflowInstanceStep = {
  step: string;
  label: string;
  operation?: string;
  operation_id?: string;
  milestone: string;
  position: number;
  /**
   * True when a matching fact is visible on the current milestone page.
   */
  observed: boolean;
  /**
   * Number of matching facts on the current milestone page.
   */
  milestones_in_page: number;
  /**
   * Latest matching fact on the current milestone page.
   */
  latest_milestone?: OperationWorkflowInstanceMilestoneRef;
  /**
   * True when at least one matching fact for the selected contract version remains under normal Operation retention. False means no matching fact is currently retained and does not prove it never occurred.
   */
  observed_in_retention: boolean;
  /**
   * Number of matching facts for the selected contract version that remain under normal Operation retention.
   */
  milestones_in_retention: number;
  /**
   * Latest matching fact for the selected contract version by platform publication time across all currently retained facts.
   */
  latest_retained_milestone?: OperationWorkflowInstanceMilestoneRef;
};

