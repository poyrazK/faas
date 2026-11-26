/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Result of one protocol or catalog check in readiness evidence.
 */
export type MCPReadinessCheck = {
  name: string;
  status: 'passed' | 'failed';
  detail?: string;
};

