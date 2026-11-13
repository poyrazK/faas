/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineAuthOrganizations } from './DataPipelineAuthOrganizations.js';
import type { DataPipelineRelationship } from './DataPipelineRelationship.js';
import type { DataPipelineTenantAccess } from './DataPipelineTenantAccess.js';
/**
 * Versioned serving contract affecting publication, mirror schema and recovery. Creation accepts version 1; approved evolution installs version 2. Omit columns for all-column replication. Tenant claims grant no memberships. Column expansion requires source-definition approval, backfill, verification and API refresh.
 */
export type DataPipelineServing = {
  version?: 1 | 2;
  /**
   * Every selected table maps to 1–128 unique columns, including all primary keys and the tenant column. Names are bounded to 63 UTF-8 bytes.
   */
  columns?: Record<string, Array<string>>;
  auth_organizations?: DataPipelineAuthOrganizations;
  relationships?: Array<DataPipelineRelationship>;
  tenant_access?: DataPipelineTenantAccess;
};

