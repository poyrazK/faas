/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRequirementsFinding } from './RouteRequirementsFinding.js';
/**
 * Per-request requirement evaluation including satisfied, violated, or unknown findings.
 */
export type RouteRequirementsResult = {
  name?: string;
  method: string;
  path: string;
  status: string;
  checks: Array<RouteRequirementsFinding>;
};

