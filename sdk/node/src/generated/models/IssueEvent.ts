/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueBreadcrumb } from './IssueBreadcrumb.js';
import type { IssueCause } from './IssueCause.js';
import type { IssueFrame } from './IssueFrame.js';
import type { IssueSymbolicationResult } from './IssueSymbolicationResult.js';
/**
 * Structured exception evidence; credential-derived account and deployment identity cannot be overridden.
 */
export type IssueEvent = {
  breadcrumbs?: Array<IssueBreadcrumb>;
  breadcrumbs_dropped?: number;
  /**
   * Sanitized operational dimensions; unknown keys are discarded. Allowed keys are component, operation, feature_flag, feature_variant, dependency, region, job_type, workflow, state, error_class, release_channel, runtime.
   */
  tags?: Record<string, string>;
  /**
   * Opt-in server mapping against this credential's deployment artifacts; requires failure-site-v1 or cause-chain-v1.
   */
  symbolication_strategy?: 'deployment-source-maps-v1';
  symbolication?: IssueSymbolicationResult;
  causes?: Array<IssueCause>;
  /**
   * Incomplete or ambiguous chain; version 3 uses outer evidence instead of a partial cause.
   */
  causes_incomplete?: boolean;
  event_id: string;
  occurred_at: string;
  exception_type: string;
  message: string;
  stack_trace?: string;
  frames?: Array<IssueFrame>;
  fingerprint_override?: string;
  /**
   * Defaults to unchanged version 1. failure-site-v1 selects version 2 and creates separate groups without migrating existing history. cause-chain-v1 selects version 3 using the deepest complete reported cause.
   */
  grouping_strategy?: 'legacy' | 'failure-site-v1' | 'cause-chain-v1';
  /**
   * For failure-site-v1, structured frames default to newest-first. Parsed Node/Python stacks infer order when omitted.
   */
  frame_order?: 'newest-first' | 'oldest-first';
  /**
   * Stable application error code, bounded to 256 UTF-8 bytes and sanitized. In failure-site-v1 it replaces the normalized message discriminator; ignored by legacy grouping.
   */
  error_code?: string;
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

