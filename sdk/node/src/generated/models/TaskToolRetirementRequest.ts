/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPAccessPolicyExpectation } from './MCPAccessPolicyExpectation.js';
import type { TaskToolRecoveryQualification } from './TaskToolRecoveryQualification.js';
/**
 * Private trusted CLI qualification, native park or read-only shutdown observation. Agent tools accept public identities only. Pending replays never issue a new park request.
 */
export type TaskToolRetirementRequest = {
  action?: 'park' | 'observe';
  qualification?: TaskToolRecoveryQualification;
  qualification_sha256?: string;
  claim_token?: string;
  bindings_sha256?: string;
  evidence_sha256?: string;
  checked_at?: string;
  shutdown_timeout_ms?: number;
  expected_instance_ids?: Record<string, Array<string>>;
  expected_mcp_access_policy?: MCPAccessPolicyExpectation;
};

