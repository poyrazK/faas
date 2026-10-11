/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPTaskHandlerCompatibility } from './MCPTaskHandlerCompatibility.js';
export type MCPTaskCompatibilityReportResponse = {
  published: boolean;
  handlers: Array<MCPTaskHandlerCompatibility>;
  truncated: boolean;
  observed_at?: string;
  freshness_seconds: number;
  stale: boolean;
};

