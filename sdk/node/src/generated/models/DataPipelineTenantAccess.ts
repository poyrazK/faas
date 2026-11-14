/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineTenantMembership } from './DataPipelineTenantMembership.js';
/**
 * Row access through a text, varchar or UUID column present on every selected table. Memberships match verified JWT sub values exactly. Empty memberships revoke every reader.
 */
export type DataPipelineTenantAccess = {
  column: string;
  /**
   * At most 128 distinct subjects. Tenant and subject values are bounded to 256 UTF-8 bytes.
   */
  memberships: Array<DataPipelineTenantMembership>;
};

