/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/apps/{slug}/deployments/{id}/cancel (ADR-124). Optional — empty body defaults to reason='user' server-side. Reason is the closed set user | auto_quota | auto_health | system.
 */
export type CancelDeploymentRequest = {
  reason?: 'user' | 'auto_quota' | 'auto_health' | 'system';
};

