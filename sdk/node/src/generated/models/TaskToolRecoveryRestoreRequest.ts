/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPAccessPolicyExpectation } from './MCPAccessPolicyExpectation.js';
/**
 * Trusted local CLI request for the exact separately owner-approved restore. Agent tools accept public identities only; private claim material and binding/evidence details are resolved locally. No automatic retry or deadline renewal.
 */
export type TaskToolRecoveryRestoreRequest = {
  /**
   * Fresh native VM IDs by approved app ID; Task worker IDs are checked separately by the trusted CLI.
   */
  expected_instance_ids?: Record<string, Array<string>>;
  operation_id: string;
  handover_id: string;
  qualification_sha256: string;
  claim_token?: string;
  bindings_sha256?: string;
  evidence_sha256?: string;
  checked_at?: string;
  expected_mcp_access_policy?: MCPAccessPolicyExpectation;
};

