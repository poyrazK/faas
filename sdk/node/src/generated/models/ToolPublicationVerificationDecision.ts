/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner approval selecting one sealed test token or managed OAuth connection, or revoking unconsumed candidate-test authority.
 */
export type ToolPublicationVerificationDecision = {
  request_sha256: string;
  /**
   * Existing ordinary secret:MCP_* app test-token reference; exclusive with credential_connection_id and forbidden for revocation
   */
  credential_secret_ref?: string;
  /**
   * Existing owner-approved execution OAuth connection; exclusive with credential_secret_ref and forbidden for revocation
   */
  credential_connection_id?: string;
  /**
   * Required explicit owner acknowledgement for this candidate to use its reviewed policy readiness credentials; forbidden on revocation
   */
  approve_readiness_roles?: boolean;
  revoke?: boolean;
};

