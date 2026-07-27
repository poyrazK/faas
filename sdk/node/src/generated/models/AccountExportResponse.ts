/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountResponse } from './AccountResponse.js';
import type { APIKeyExportResponse } from './APIKeyExportResponse.js';
import type { AppResponse } from './AppResponse.js';
import type { AppSecretExportResponse } from './AppSecretExportResponse.js';
import type { BuildExportResponse } from './BuildExportResponse.js';
import type { CronResponse } from './CronResponse.js';
import type { CustomDomainResponse } from './CustomDomainResponse.js';
import type { DeploymentResponse } from './DeploymentResponse.js';
import type { GdprAuditExportResponse } from './GdprAuditExportResponse.js';
import type { InstanceResponse } from './InstanceResponse.js';
import type { UsageExportResponse } from './UsageExportResponse.js';
/**
 * GDPR export bundle: the account itself, every owned app, deployment, build, instance, usage record, domain, cron, API key, and sealed-secret envelope, plus the audit trail.
 */
export type AccountExportResponse = {
  exported_at: string;
  account: AccountResponse;
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

