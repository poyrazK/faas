/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteAssignedCheck } from './RouteAssignedCheck.js';
/**
 * Captured operation assignment and group provenance for checks in the same report row.
 */
export type RouteAssignment = {
  method: string;
  path: string;
  scope: string;
  groups?: Array<string>;
  checks?: Array<RouteAssignedCheck>;
};

