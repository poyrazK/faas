/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An audited permission for one breaking or unverified candidate.
 */
export type MCPContractOverride = {
  id: string;
  reason: string;
  created_by: string;
  created_at: string;
  expires_at: string;
  /**
   * The only deployment the override admits, when bound.
   */
  deployment_id?: string;
};

