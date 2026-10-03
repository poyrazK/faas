/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCheckChanges } from './RouteCheckChanges.js';
import type { RouteRequirementsCheck } from './RouteRequirementsCheck.js';
/**
 * Immutable retained completion evidence. Historical verdicts do not establish current safety. May expire under per-deployment retention caps.
 */
export type RouteCheckHistoryEntry = {
  version: number;
  id: string;
  checked_at: string;
  check: RouteRequirementsCheck;
  changes: RouteCheckChanges;
};

