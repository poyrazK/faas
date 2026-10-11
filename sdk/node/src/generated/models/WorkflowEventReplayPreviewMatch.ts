/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A retained event whose captured workflow trigger matched, with routing and durable admission state.
 */
export type WorkflowEventReplayPreviewMatch = {
  event_id: string;
  event_source: string;
  event_type: string;
  schema_version?: string;
  accepted_at: string;
  /**
   * The immutable event snapshot contains this workflow recipient.
   */
  original_recipient: 'captured';
  /**
   * Latest retained routing checkpoint for this captured workflow recipient.
   */
  routing_state: 'pending' | 'processing' | 'filtered' | 'enqueued' | 'failed';
  /**
   * The captured workflow trigger filter matches the retained envelope.
   */
  filter_matched: boolean;
  /**
   * A durable workflow admission receipt exists. This is the event/recipient deduplication key even if its run was later pruned.
   */
  admission_recorded: boolean;
  /**
   * Run ID while the linked workflow run remains retained.
   */
  workflow_run_id?: string;
  /**
   * Current retained workflow run state when the run still exists.
   */
  workflow_run_status?: string;
  receipt_url: string;
};

