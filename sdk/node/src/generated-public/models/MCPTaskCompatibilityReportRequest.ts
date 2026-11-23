/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPTaskHandlerCompatibility } from './MCPTaskHandlerCompatibility.js';
/**
 * Bounded aggregate handler inventory published by the task observer.
 */
export type MCPTaskCompatibilityReportRequest = {
  handlers: Array<MCPTaskHandlerCompatibility>;
  truncated?: boolean;
};

