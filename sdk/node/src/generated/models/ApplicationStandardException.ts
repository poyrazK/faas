/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical approval. Status uses the server as_of timestamp, with revocation taking precedence over expiry; applicability depends on current adoption.
 */
export type ApplicationStandardException = {
  id: string;
  org_id: string;
  app_id: string;
  standard_id: string;
  version: number;
  field: 'log_destinations' | 'require_signed' | 'security_policy' | 'trusted_publishers' | 'egress_cidrs' | 'egress_extra_ports';
  value: (boolean | 'off' | 'audit' | 'enforce' | Array<string> | Array<number>);
  reason: string;
  expires_at: string;
  approved_by: string;
  created_at: string;
  revoked_by?: string;
  revoked_at?: string;
  status: 'active' | 'expired' | 'revoked';
};

