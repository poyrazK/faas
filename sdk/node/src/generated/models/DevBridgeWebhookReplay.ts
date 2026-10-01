/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable outcome of one development copy, separate from original delivery.
 */
export type DevBridgeWebhookReplay = {
  id: string;
  session_id: string;
  invocation_id: string;
  state: 'dispatching' | 'completed' | 'uncertain';
  http_status: number;
  created_at: string;
  completed_at?: string | null;
};

