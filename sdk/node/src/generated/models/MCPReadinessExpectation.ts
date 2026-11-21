/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact fresh role observations required in the traffic transaction, independently of emergency overrides. Mismatches return 409 without changing traffic.
 */
export type MCPReadinessExpectation = {
  policy_revision: string;
  artifact_sha256: string;
  evidence_ids: Record<string, string>;
};

