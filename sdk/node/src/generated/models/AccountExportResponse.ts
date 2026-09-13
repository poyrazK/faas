/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountResponse } from './AccountResponse.js';
import type { APIKeyExportResponse } from './APIKeyExportResponse.js';
import type { APIKeyResponse } from './APIKeyResponse.js';
import type { AppResponse } from './AppResponse.js';
import type { AppSecretExportResponse } from './AppSecretExportResponse.js';
import type { BuildExportResponse } from './BuildExportResponse.js';
import type { CronResponse } from './CronResponse.js';
import type { CustomDomainResponse } from './CustomDomainResponse.js';
import type { DeploymentResponse } from './DeploymentResponse.js';
import type { GdprAuditExportResponse } from './GdprAuditExportResponse.js';
import type { InstanceResponse } from './InstanceResponse.js';
import type { OrgInvitationResponse } from './OrgInvitationResponse.js';
import type { OrgMembershipExportResponse } from './OrgMembershipExportResponse.js';
import type { OrgResponse } from './OrgResponse.js';
import type { UsageExportResponse } from './UsageExportResponse.js';
/**
 * Versioned GDPR export bundle: the account and organization identity graph, owned runtime resources, redacted key metadata, sealed-secret envelopes, and audit trail.
 */
export type AccountExportResponse = {
  /**
   * Export schema version used by restore and portability tooling.
   */
  schema_version: 2;
  exported_at: string;
  account: AccountResponse;
  organizations: Array<OrgResponse>;
  org_memberships: Array<OrgMembershipExportResponse>;
  org_invitations: Array<OrgInvitationResponse>;
  /**
   * Org-bound API key metadata; plaintext and hashes are never exported.
   */
  org_api_keys: Array<APIKeyResponse>;
  apps: Array<AppResponse>;
  deployments: Array<DeploymentResponse>;
  builds: Array<BuildExportResponse>;
  instances: Array<InstanceResponse>;
  usage: Array<UsageExportResponse>;
  domains: Array<CustomDomainResponse>;
  crons: Array<CronResponse>;
  api_keys: Array<APIKeyExportResponse>;
  app_secrets: Array<AppSecretExportResponse>;
  audit_trail?: Array<GdprAuditExportResponse>;
};

