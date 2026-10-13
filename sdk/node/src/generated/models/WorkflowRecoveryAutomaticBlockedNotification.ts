/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * First observed evidence block capture for this automatic action. Receivers are frozen at capture; queued identifies ledger admission rather than successful delivery. No receivers prevents historical replay to later subscribers.
 */
export type WorkflowRecoveryAutomaticBlockedNotification = {
  event_id: string;
  status: 'queued' | 'no_receivers';
  reason: 'evidence_unknown';
  captured_at: string;
  receiver_webhook_ids: Array<string>;
};

