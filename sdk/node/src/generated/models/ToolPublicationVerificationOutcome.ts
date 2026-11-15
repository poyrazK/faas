/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolPublicationRoleResult } from './ToolPublicationRoleResult.js';
/**
 * Platform-observed catalog and named test results without credentials or upstream response data.
 */
export type ToolPublicationVerificationOutcome = {
  ok: boolean;
  protocol_verified: boolean;
  /**
   * Canonical verified catalog; absent on failure
   */
  catalog_base64?: string;
  checks: Array<{
    name: string;
    status: 'passed' | 'failed';
  }>;
  readiness?: Array<ToolPublicationRoleResult>;
  /**
   * Stable verification failure stage without upstream response data
   */
  code?: string;
};

