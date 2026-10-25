/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowCoordinationTimelineEvent } from './OperationWorkflowCoordinationTimelineEvent.js';
import type { OperationWorkflowCoordinationTimelineSource } from './OperationWorkflowCoordinationTimelineSource.js';
/**
 * OperationWorkflowCoordinationTimeline. Retained inbox coordination projection metadata.
 */
export type OperationWorkflowCoordinationTimeline = {
  /**
   * OperationWorkflowCoordinationTimeline.items. Chronological page ordered by retained server clock then stable event identity.
   */
  items: Array<OperationWorkflowCoordinationTimelineEvent>;
  /**
   * OperationWorkflowCoordinationTimeline.sources. Coverage of current subscriptions within the authenticated role, app and environment.
   */
  sources: Array<OperationWorkflowCoordinationTimelineSource>;
  /**
   * OperationWorkflowCoordinationTimeline.subject. Exact requested business subject.
   */
  subject: OperationSubject;
  /**
   * OperationWorkflowCoordinationTimeline.workflow. Exact requested workflow name.
   */
  workflow: string;
  /**
   * OperationWorkflowCoordinationTimeline.instance_id. Exact requested workflow instance.
   */
  instance_id: string;
  /**
   * OperationWorkflowCoordinationTimeline.scope. Application environment.
   */
  scope: string;
  /**
   * OperationWorkflowCoordinationTimeline.platform_tenant_id. Operator-only exact customer selection; omitted for account-owned workflows.
   */
  platform_tenant_id?: string;
  /**
   * OperationWorkflowCoordinationTimeline.evaluated_at. Server clock after independent source reads.
   */
  evaluated_at: string;
  /**
   * OperationWorkflowCoordinationTimeline.total_count. Matching retained event count before live pagination.
   */
  total_count: number;
  /**
   * OperationWorkflowCoordinationTimeline.next_offset. Offset for the next live page.
   */
  next_offset: number;
  /**
   * OperationWorkflowCoordinationTimeline.has_more. More events existed at evaluation.
   */
  has_more: boolean;
  /**
   * OperationWorkflowCoordinationTimeline.retention_limited. Always true: deleted sources and pruned records cannot be reconstructed.
   */
  retention_limited: boolean;
  /**
   * OperationWorkflowCoordinationTimeline.sources_truncated. Current source list exceeded the bounded projection.
   */
  sources_truncated: boolean;
  /**
   * OperationWorkflowCoordinationTimeline.history_truncated. A matching response target lost older assignment history.
   */
  history_truncated: boolean;
  /**
   * OperationWorkflowCoordinationTimeline.limitations. Explicit retention, snapshot and pagination limitations.
   */
  limitations: Array<string>;
};

