/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed wiring mode when every serving member has a fresh report. Disagreement reports mixed, including shared retry backend endpoint disagreement. Missing or stale members leave the feature unverified.
 */
export type TrafficRuntimeFeatureStatus = {
  state: 'observed' | 'mixed' | 'unverified';
  mode: 'enabled' | 'disabled' | 'central' | 'local' | 'shared' | 'redis' | 'mixed' | 'unwired';
};

