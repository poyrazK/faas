/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineServing } from './DataPipelineServing.js';
/**
 * Immutable durable setup intent. Repeating exactly the same normalized name and selections recovers the saved job and resource IDs. Source discovery must already include the selected tables; the worker refreshes credentials and capacity before allocating resources.
 */
export type DataPipelineSetupRequest = {
  name: string;
  source_connection_id: string;
  database_id?: string;
  region?: string;
  tables: Array<string>;
  read_subjects?: Array<string>;
  serving?: DataPipelineServing;
  /**
   * DataPipelineSetupRequest uses zero to disable confirmation alerts; enabled targets must be at least 15 seconds.
   */
  freshness_target_seconds?: number;
  data_api_name?: string;
  auth_app_id?: string;
};

