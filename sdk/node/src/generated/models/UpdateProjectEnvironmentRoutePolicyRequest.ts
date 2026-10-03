/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeclaredRoute } from './DeclaredRoute.js';
/**
 * Complete replacement for a workload's environment route contract. Enabling enforcement requires a non-empty explicit route list; application-wide OpenAPI documents are not cloned.
 */
export type UpdateProjectEnvironmentRoutePolicyRequest = {
  only_allow_declared_routes: boolean;
  declared_routes: Array<DeclaredRoute>;
};

