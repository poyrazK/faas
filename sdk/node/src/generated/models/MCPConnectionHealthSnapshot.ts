/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPConnectionHealthIssue } from './MCPConnectionHealthIssue.js';
/**
 * Account-wide or app-filtered connection issues with complete counts independent of issue pagination. Incomplete inventory cannot prove alert recovery.
 */
export type MCPConnectionHealthSnapshot = {
  observed_at: string;
  complete: boolean;
  counts: Record<string, number>;
  total_issues: number;
  items: Array<MCPConnectionHealthIssue>;
  next_offset?: number;
};

