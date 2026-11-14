/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Admission-time release intent retained across routing, failure and stage changes. Absent on legacy deployments.
 */
export type DeploymentRetryIntent = {
  version: 'v1';
  traffic_percent: number;
  traffic_percent_explicit: boolean;
  service_rollout: boolean;
};

