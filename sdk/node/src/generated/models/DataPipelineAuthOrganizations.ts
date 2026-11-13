/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineOrganizationTenant } from './DataPipelineOrganizationTenant.js';
/**
 * Owner-approved Auth organization mapping. Internal binding identifiers are resolved by the control plane and rechecked on every snapshot.
 */
export type DataPipelineAuthOrganizations = {
  auth_app_id: string;
  readonly binding_id?: string;
  readonly database_id?: string;
  mappings: Array<DataPipelineOrganizationTenant>;
};

