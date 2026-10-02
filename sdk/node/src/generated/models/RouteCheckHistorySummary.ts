/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCheckChangeSummary } from './RouteCheckChangeSummary.js';
/**
 * Compact completion metadata; use its ID to read retained full evidence.
 */
export type RouteCheckHistorySummary = {
  version: number;
  id: string;
  checked_at: string;
  status: 'satisfied' | 'violated' | 'unknown';
  requirements_revision: number;
  requirements_sha256: string;
  comparison_status: 'initial' | 'comparable' | 'requirements_changed' | 'unavailable' | 'ambiguous' | 'tracking_limit';
  summary: RouteCheckChangeSummary;
};

