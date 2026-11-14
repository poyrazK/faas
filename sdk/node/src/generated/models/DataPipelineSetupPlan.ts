/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataConnectionCheck } from './DataConnectionCheck.js';
/**
 * Read-only resource and billing preview. No mirror or app is allocated and no capacity is reserved. Limits and billing implications are not a fixed-price invoice; source-provider compute charges are separate.
 */
export type DataPipelineSetupPlan = {
  name: string;
  creates_mirror: boolean;
  creates_data_api: boolean;
  existing_setup_id?: string;
  database_id?: string;
  region?: string;
  storage_limit_bytes?: number;
  restore_window_seconds?: number;
  /**
   * DataPipelineSetupPlan uses zero to disable confirmation alerts; enabled targets must be at least 15 seconds.
   */
  freshness_target_seconds?: number;
  connection_check?: DataConnectionCheck;
  billing_notes: Array<string>;
};

