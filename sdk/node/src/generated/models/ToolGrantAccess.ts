/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Currently active grant for this connection and exact described tool; omits other tools and owner-only grant metadata.
 */
export type ToolGrantAccess = {
  /**
   * Opaque reference required for this private invocation grant
   */
  credential_binding_sha256?: string;
  grant_id: string;
  expires_at: string;
  allow_invocation: boolean;
};

