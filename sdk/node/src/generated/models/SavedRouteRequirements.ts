/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRequirementsConfig } from './RouteRequirementsConfig.js';
/**
 * Current version 2 route intent. Revision increments only when normalized intent changes. Original public rationale is never stored or returned.
 */
export type SavedRouteRequirements = {
  app_id: string;
  revision: number;
  sha256: string;
  requirements: RouteRequirementsConfig;
  updated_at: string;
};

