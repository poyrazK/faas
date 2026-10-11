/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Earliest unresolved routing recipient in the captured ordered lane; excludes event data and resolved work keys.
 */
export type EventOrderingBlocker = {
  event_source: string;
  event_id: string;
  subscription_id: string;
  accepted_at: string;
  state: 'pending' | 'processing';
  next_attempt_at?: string;
  age_seconds: number;
  receipt_url: string;
};

