/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineTenantAccess } from './DataPipelineTenantAccess.js';
/**
 * Versioned serving contract. Columns are immutable and affect publication, mirror schema and recovery. Omit columns for all-column replication. Tenant access uses owner-managed subject memberships; JWT tenant claims grant nothing. At least columns or tenant_access is required. A missing version defaults to 1 at creation.
 */
export type DataPipelineServing = {
  version?: 1;
  /**
   * Every selected table maps to 1–128 unique columns, including all primary keys and the tenant column. Names are bounded to 63 UTF-8 bytes.
   */
  columns?: Record<string, Array<string>>;
  tenant_access?: DataPipelineTenantAccess;
};

