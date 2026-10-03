/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCheckChangeSummary } from './RouteCheckChangeSummary.js';
import type { RouteFindingChange } from './RouteFindingChange.js';
/**
 * Deterministic bounded comparison with exact summary counts. Initial checks and intent changes establish a baseline without claiming resolution. Truncated detail never changes summary counts. Previous observations can outlive retained history.
 */
export type RouteCheckChanges = {
  version: number;
  check_id: string;
  compared_to_check_id?: string;
  status: 'initial' | 'comparable' | 'requirements_changed' | 'unavailable' | 'ambiguous' | 'tracking_limit';
  summary: RouteCheckChangeSummary;
  truncated: boolean;
  findings: Array<RouteFindingChange>;
};

