/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact workflow-only observation. Opaque version invalidates on every update; state is stored triage metadata, not health or effective inbox visibility.
 */
export type DebugRegressionReview = {
  app_id: string;
  deployment_id: string;
  route: string;
  version: string;
  state: 'active' | 'acknowledged' | 'dismissed' | 'resolved';
  first_detected_at: string;
  last_detected_at: string;
  acknowledged_at?: string;
  dismissed_until?: string;
  resolved_at?: string;
};

