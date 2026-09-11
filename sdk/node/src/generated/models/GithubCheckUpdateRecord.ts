/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Queue metadata for one outbound GitHub Check Run update.
 */
export type GithubCheckUpdateRecord = {
  deployment_id: string;
  generation: number;
  status: 'pending' | 'processing' | 'succeeded' | 'dead';
  attempts: number;
  next_attempt_at: string;
  last_error?: string;
  processed_at?: string | null;
  updated_at: string;
};

