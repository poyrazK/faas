/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAuditActivityRow } from './ObsAuditActivityRow.js';
import type { ObsInvocationRow } from './ObsInvocationRow.js';
/**
 * Tenant activity metadata - invocation and audit rows, never payloads.
 */
export type ObsTenantActivityResponse = {
  account_id: string;
  generated_at: string;
  invocations: Array<ObsInvocationRow>;
  audit_events: Array<ObsAuditActivityRow>;
  limit: number;
};

