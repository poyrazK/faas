/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueFrame } from './IssueFrame.js';
/**
 * Structured exception evidence; credential-derived account and deployment identity cannot be overridden.
 */
export type IssueEvent = {
  event_id: string;
  occurred_at: string;
  exception_type: string;
  message: string;
  stack_trace?: string;
  frames?: Array<IssueFrame>;
  fingerprint_override?: string;
  trace_id?: string;
  span_id?: string;
  /**
   * Bounded opaque gateway request ID, sanitized before persistence.
   */
  request_id?: string;
  invocation_id?: string;
  route?: string;
  http_status?: number;
  source_kind?: string;
  redactions?: Array<string>;
};

