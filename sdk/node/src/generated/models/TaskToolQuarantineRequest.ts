/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPAccessPolicyExpectation } from './MCPAccessPolicyExpectation.js';
import type { TaskToolRecoveryQualification } from './TaskToolRecoveryQualification.js';
/**
 * Private trusted CLI qualification or attempt metadata on existing journal routes. Agent tools accept public identities only. A claim is never automatically retried; observe records read-only fence evidence after an admitted attempt.
 */
export type TaskToolQuarantineRequest = {
  action?: 'claim' | 'observe';
  qualification?: TaskToolRecoveryQualification;
  qualification_sha256?: string;
  claim_token?: string;
  attempt_token?: string;
  bindings_sha256?: string;
  evidence_sha256?: string;
  checked_at?: string;
  expected_instance_ids?: Record<string, Array<string>>;
  expected_mcp_access_policy?: MCPAccessPolicyExpectation;
};

