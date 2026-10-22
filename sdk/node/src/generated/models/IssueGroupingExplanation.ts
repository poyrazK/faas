/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueGroupingFrame } from './IssueGroupingFrame.js';
/**
 * Read-only evidence reconstructed from a retained sanitized occurrence using the saved group version. Present only when its identity reproduces the saved fingerprint. Unsupported versions or mismatches omit the explanation.
 */
export type IssueGroupingExplanation = {
  /**
   * Zero-based selected cause index; absent when grouping uses the outer exception.
   */
  cause_index?: number;
  grouping_version: number;
  strategy: 'legacy' | 'failure-site-v1' | 'cause-chain-v1';
  basis: 'override' | 'application-frames' | 'failure-site' | 'fallback';
  source_kind: string;
  exception_type: string;
  frames?: Array<IssueGroupingFrame>;
  route?: string;
  http_status?: number;
  discriminator_kind: 'none' | 'override' | 'error-code' | 'message';
  /**
   * Selected sanitized override, stable error code, or normalized message. May be empty for an empty message.
   */
  discriminator_value?: string;
};

