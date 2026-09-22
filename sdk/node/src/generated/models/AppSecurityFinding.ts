/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One actionable security posture finding. Codes beginning with image_scan_ describe live-image scan-evidence coverage.
 */
export type AppSecurityFinding = {
  code: string;
  severity: 'critical' | 'high' | 'medium' | 'low' | 'info';
  title: string;
  detail: string;
  remediation: string;
};

