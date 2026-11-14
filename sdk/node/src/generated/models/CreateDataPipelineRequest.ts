/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineServing } from './DataPipelineServing.js';
/**
 * Selected public tables, dedicated destination and explicitly authorized JWT readers. Supply read_subjects for whole-table access, or serving.tenant_access with owner-managed memberships and no read_subjects. Explicit columns must cover every table and retain all primary-key and tenant columns. A fresh passing connection check for the current source credential and all selected tables is required; otherwise returns data_connection_check_required.
 */
export type CreateDataPipelineRequest = {
  serving?: DataPipelineServing;
  name: string;
  source_connection_id: string;
  database_id: string;
  tables: Array<string>;
  read_subjects?: Array<string>;
};

