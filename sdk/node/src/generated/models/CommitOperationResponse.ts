/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type CommitOperationResponse = {
  id: string;
  receipt_id: string;
  source_id: string;
  event_id: string;
  state: 'accepted' | 'running' | 'completed' | 'cancelled' | 'failed' | 'unknown';
  accepted_at: string;
  completed_at?: string;
};

