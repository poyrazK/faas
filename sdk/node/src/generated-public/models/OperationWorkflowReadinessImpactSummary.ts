/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessImpactOwner } from './OperationWorkflowReadinessImpactOwner.js';
/**
 * OperationWorkflowReadinessImpactSummary. Retained unresolved review confidence by workflow and protected business result, separate from reported business outcome.
 */
export type OperationWorkflowReadinessImpactSummary = {
  /**
   * OperationWorkflowReadinessImpactSummary.workflow. Scoped workflow identity; identical result codes in different workflows remain separate.
   */
  workflow: string;
  /**
   * OperationWorkflowReadinessImpactSummary.kind. Pinned protected result category.
   */
  kind: 'milestone' | 'outcome';
  /**
   * OperationWorkflowReadinessImpactSummary.code. Pinned protected milestone or outcome code.
   */
  code: string;
  /**
   * OperationWorkflowReadinessImpactSummary.review_count. Distinct progress/outcome/step/regression obligations across inbox deliveries.
   */
  review_count: number;
  /**
   * OperationWorkflowReadinessImpactSummary.task_count. Matching retained inbox delivery tasks.
   */
  task_count: number;
  /**
   * OperationWorkflowReadinessImpactSummary.case_count. Distinct tenant/subject/workflow/instance cases.
   */
  case_count: number;
  /**
   * OperationWorkflowReadinessImpactSummary.critical_count. Distinct critical review obligations.
   */
  critical_count: number;
  /**
   * OperationWorkflowReadinessImpactSummary.high_count. Distinct high-priority review obligations.
   */
  high_count: number;
  /**
   * OperationWorkflowReadinessImpactSummary.overdue_count. Distinct review obligations with at least one overdue queue delivery.
   */
  overdue_count: number;
  /**
   * OperationWorkflowReadinessImpactSummary.critical_overdue_count. Distinct critical review obligations with overdue queue attention.
   */
  critical_overdue_count: number;
  /**
   * OperationWorkflowReadinessImpactSummary.owners. Effective inbox ownership totals, ordered by overdue task count, task count and owner label. A review delivered to several teams contributes to each delivery's owner.
   */
  owners: Array<OperationWorkflowReadinessImpactOwner>;
};

