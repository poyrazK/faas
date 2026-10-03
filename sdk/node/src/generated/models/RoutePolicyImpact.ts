/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RoutePolicyAffectedOperation } from './RoutePolicyAffectedOperation.js';
/**
 * Platform host, method and concrete or family path whose policy selection can change. Captured impact is bounded to the selected contract; wildcard rules can also affect uncaptured paths.
 */
export type RoutePolicyImpact = {
  host: string;
  method: string;
  path: string;
  scope: string;
  beyond_capture?: boolean;
  captured?: Array<RoutePolicyAffectedOperation>;
};

