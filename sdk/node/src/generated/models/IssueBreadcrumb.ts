/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded sanitized event context recorded before an issue occurrence.
 */
export type IssueBreadcrumb = {
  occurred_at: string;
  category: 'http' | 'db' | 'cache' | 'workflow' | 'log' | 'custom';
  level: 'debug' | 'info' | 'warning' | 'error';
  /**
   * Sanitized and bounded to 256 UTF-8 bytes.
   */
  message: string;
};

