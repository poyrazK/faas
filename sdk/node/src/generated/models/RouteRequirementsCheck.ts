/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteRequirementsReport } from './RouteRequirementsReport.js';
/**
 * Read-only coverage against one captured deployment and current configuration with saved intent provenance. Does not persist history or prove runtime behavior.
 */
export type RouteRequirementsCheck = {
  version: number;
  app: string;
  app_id: string;
  deployment_id: string;
  requirements_revision: number;
  requirements_sha256: string;
  configuration_sha256: string;
  report: RouteRequirementsReport;
};

