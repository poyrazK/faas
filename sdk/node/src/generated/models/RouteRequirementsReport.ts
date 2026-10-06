/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteAssignment } from './RouteAssignment.js';
import type { RouteCoverageInventory } from './RouteCoverageInventory.js';
import type { RouteGroupResult } from './RouteGroupResult.js';
import type { RouteRequirementsResult } from './RouteRequirementsResult.js';
/**
 * Configuration evidence for concrete requests or every captured operation with group assignments and bounded policy scope.
 */
export type RouteRequirementsReport = {
  version: number;
  sha256: string;
  host?: string;
  policy_scope: string;
  status: string;
  scope: string;
  routes: Array<RouteRequirementsResult>;
  coverage?: RouteCoverageInventory;
  groups?: Array<RouteGroupResult>;
  assignments?: Array<RouteAssignment>;
};

