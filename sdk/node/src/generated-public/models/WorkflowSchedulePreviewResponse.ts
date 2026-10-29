/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowScheduleCatchUpPreview } from './WorkflowScheduleCatchUpPreview.js';
import type { WorkflowScheduleDSTBehavior } from './WorkflowScheduleDSTBehavior.js';
import type { WorkflowScheduleFirePreview } from './WorkflowScheduleFirePreview.js';
/**
 * Read-only schedule simulation using the deployed trigger and durable cursor. Upcoming times do not guarantee capacity or execution.
 */
export type WorkflowSchedulePreviewResponse = {
  workflow_name: string;
  deployment_id: string;
  schedule: string;
  /**
   * Effective IANA timezone.
   */
  timezone: string;
  /**
   * Configured overlap policy. Active runs may still block admission when overlap is skip.
   */
  overlap: 'skip' | 'allow';
  enabled: boolean;
  observed_at: string;
  /**
   * Actual or hypothetical evaluator time.
   */
  evaluation_at: string;
  dst_behavior: WorkflowScheduleDSTBehavior;
  upcoming: Array<WorkflowScheduleFirePreview>;
  catch_up: WorkflowScheduleCatchUpPreview;
};

