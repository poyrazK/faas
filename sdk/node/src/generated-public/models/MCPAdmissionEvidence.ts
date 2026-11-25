/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPAdmissionGateway } from './MCPAdmissionGateway.js';
/**
 * Version 1 durable traffic admission snapshot in deployment audit data.mcp_admission. Accepted commits with traffic; rejected is saved after rollback. Existing deployment audit retention applies. This is historical admission evidence, not a lease or live authorization proof.
 */
export type MCPAdmissionEvidence = {
  version: 1;
  id: string;
  deployment_id: string;
  traffic_percent: number;
  captured_at: string;
  outcome: 'accepted' | 'rejected';
  /**
   * Public rejection code, or traffic_update_failed for a later traffic failure. Raw errors are never stored.
   */
  reason?: string;
  expected_fingerprint: string;
  observed_fingerprint?: string;
  expected_revision?: number;
  observed_revision?: number;
  gateways: Array<MCPAdmissionGateway>;
};

