/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An organization's agent policy.
 */
export type OrgAgentPolicyResponse = {
  /**
   * Whether agents may act in the organization.
   */
  agents_allowed: boolean;
  /**
   * The capabilities agents may carry beyond inspection.
   */
  allowed_scopes: Array<string>;
  /**
   * When the policy last changed; absent for an organization that never set one.
   */
  updated_at?: string;
  /**
   * Agent connections a change revoked because the new policy no longer allows them.
   */
  revoked?: number;
};

