/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowCoordinationTimelineEvent. Retained inbox coordination projection metadata.
 */
export type OperationWorkflowCoordinationTimelineEvent = {
  /**
   * OperationWorkflowCoordinationTimelineEvent.id. Stable identity within its subscription and notification.
   */
  id: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.at. Retained server observation or action clock; never inferred application occurrence time.
   */
  at: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.kind. Notification, retained action, outcome observation or latest coordination snapshot.
   */
  kind: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.summary. Explanation of the retained event and any evidence limitation.
   */
  summary: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.subscription_id. Source readiness subscription UUID.
   */
  subscription_id: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.subscription_name. Current source subscription name.
   */
  subscription_name: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.subscription_tenant_id. Operator-only original subscription tenant used to navigate the source inbox; omitted for broad subscriptions.
   */
  subscription_tenant_id?: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.notification_id. Source inbox notification UUID.
   */
  notification_id: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.sequence. Source inbox sequence.
   */
  sequence: number;
  /**
   * OperationWorkflowCoordinationTimelineEvent.actor. Retained coordination actor, when available.
   */
  actor?: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.owner. Retained coordination owner label.
   */
  owner?: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.status. Status in the event kind context.
   */
  status?: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.note. Retained human coordination note.
   */
  note?: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.follow_up_at. Associated follow-up or reminder due clock.
   */
  follow_up_at?: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.expected_outcome_code. Application-declared expected business outcome.
   */
  expected_outcome_code?: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.reported_outcome_code. Observed application-reported business outcome.
   */
  reported_outcome_code?: string;
  /**
   * OperationWorkflowCoordinationTimelineEvent.state_revision. Observed workflow report revision.
   */
  state_revision?: number;
  /**
   * OperationWorkflowCoordinationTimelineEvent.reporting_operation_ids. Available operation evidence links; absence does not imply success or failure.
   */
  reporting_operation_ids: Array<string>;
};

