/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRequirementsFinding } from './RouteRequirementsFinding.js';
/**
 * One finding compared with its last known observation for unchanged saved intent. Removed findings are not fixes; unknown evidence retains the previous known verdict.
 */
export type RouteFindingChange = {
  method: string;
  path: string;
  requirement: string;
  kind: 'newly_violated' | 'resolved' | 'changed' | 'unknown' | 'removed' | 'observed';
  before_check_id?: string;
  before?: RouteRequirementsFinding;
  after?: RouteRequirementsFinding;
};

