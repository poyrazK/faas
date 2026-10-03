/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueHandoffSpan } from './IssueHandoffSpan.js';
/**
 * Uniquely correlated singleton telemetry within the account's plan retention, with at most eight slowest sanitized spans. No arbitrary attributes or SQL statements are forwarded.
 */
export type IssueHandoffRequest = {
  id: string;
  trace_id?: string;
  route: string;
  method: string;
  status: number;
  latency_ms: number;
  cold_boot: boolean;
  received_at: string;
  spans: Array<IssueHandoffSpan>;
  spans_truncated: boolean;
};

