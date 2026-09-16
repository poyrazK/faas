/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Redacted webhook processing activity for the bound GitHub app.
 */
export type GitHubWebhookActivity = {
  event_type: string;
  status: 'pending' | 'processing' | 'succeeded' | 'dead';
  commit_sha?: string;
  received_at: string;
  processed_at?: string | null;
  updated_at: string;
};

