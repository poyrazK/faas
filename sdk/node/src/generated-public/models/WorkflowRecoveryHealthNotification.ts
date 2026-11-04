/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Frozen health notification identity and receiver set; no_receivers is terminal for this transition and does not replay to late receivers.
 */
export type WorkflowRecoveryHealthNotification = {
  event_id: string;
  status: 'queued' | 'no_receivers';
  captured_at: string;
  receiver_webhook_ids: Array<string>;
};

