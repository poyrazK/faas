/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRequirementsConfig } from './RouteRequirementsConfig.js';
export type SaveRouteRequirementsRequest = {
  /**
   * Current revision; 0 creates the first saved record.
   */
  expected_revision: number;
  requirements: RouteRequirementsConfig;
};

