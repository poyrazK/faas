/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Principal send outcome including fleet queueing and retained notification acceptance.
 */
export type ManagedRealtimePrincipalSendResponse = {
  message_id?: string;
  sequence?: number;
  durable?: boolean;
  fallback_deadline?: string;
  recipients?: number;
  queued?: number;
  unsupported?: number;
  queue_full?: number;
  failed?: number;
  nodes_queried?: number;
  nodes_unavailable?: number;
  partial?: boolean;
  receipt_requested?: boolean;
  receipt_status?: string;
  acknowledged?: number;
  pending?: number;
  timed_out?: number;
};

