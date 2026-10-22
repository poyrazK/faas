/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable compatibility code and sanitized guidance; contains neither credentials nor raw database errors.
 */
export type DataConnectionIssue = {
  code: string;
  state: 'passed' | 'blocked' | 'unknown';
  message: string;
  remediation?: string;
  column?: string;
};

