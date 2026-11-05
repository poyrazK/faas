/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowCoordinationTimelineSource. Retained inbox coordination projection metadata.
 */
export type OperationWorkflowCoordinationTimelineSource = {
  /**
   * OperationWorkflowCoordinationTimelineSource.subscription_id. Read subscription UUID.
   */
  subscription_id: string;
  /**
   * OperationWorkflowCoordinationTimelineSource.name. Current subscription name.
   */
  name: string;
  /**
   * OperationWorkflowCoordinationTimelineSource.earliest_sequence. Earliest retained sequence, or zero when empty.
   */
  earliest_sequence: number;
  /**
   * OperationWorkflowCoordinationTimelineSource.latest_sequence. Latest published sequence at source read.
   */
  latest_sequence: number;
  /**
   * OperationWorkflowCoordinationTimelineSource.retained_count. Notifications retained in this source, including other workflow identities.
   */
  retained_count: number;
  /**
   * OperationWorkflowCoordinationTimelineSource.gap_before. Older notifications were pruned or an inbox resynchronization is required.
   */
  gap_before: boolean;
  /**
   * OperationWorkflowCoordinationTimelineSource.missing_during_read. Subscription disappeared between listing and reading.
   */
  missing_during_read: boolean;
};

