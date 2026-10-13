/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueFrame } from './IssueFrame.js';
/**
 * One sanitized linear cause, ordered immediate to deepest; bounds are UTF-8 bytes.
 */
export type IssueCause = {
  exception_type: string;
  message: string;
  error_code?: string;
  frames?: Array<IssueFrame>;
  frame_order?: 'newest-first' | 'oldest-first';
};

