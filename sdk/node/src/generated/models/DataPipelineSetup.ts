/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineSetupRequest } from './DataPipelineSetupRequest.js';
/**
 * Account-owned durable setup progress with public recovery IDs. Cancellation retains allocated resources for separate management. Private provider plans and credentials are never returned.
 */
export type DataPipelineSetup = {
  id: string;
  request: DataPipelineSetupRequest;
  database_id: string;
  pipeline_id?: string;
  state: string;
  phase: string;
  reason?: string;
  revision: number;
  data_api_app_id?: string;
  deployment_id?: string;
  endpoint?: string;
  region?: string;
  storage_limit_bytes?: number;
  progress: string;
  recovery?: string;
  created_at: string;
  updated_at: string;
};

