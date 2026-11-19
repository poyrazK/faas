/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Eligible succeeded builds without an image-preparation checkpoint in this instance's recovery scope. Oldest finish time can precede the rollout. Zero work omits that time; ages allow the existing clock skew and never imply a stalled worker or a ready deployment.
 */
export type ObsRuntimeHandoffBacklogValues = {
  pending: number;
  oldest_finished_at?: string;
  oldest_wait_seconds: number;
};

