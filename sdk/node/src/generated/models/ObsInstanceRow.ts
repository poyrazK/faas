/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One instance in the operator app-detail view.
 */
export type ObsInstanceRow = {
  id: string;
  app_id: string;
  app_slug?: string;
  account_id?: string;
  deployment_id: string;
  node_id?: string;
  node_name?: string;
  state: string;
  ram_mb: number;
  started_at: string;
  last_request_at: string;
  parked_at?: string | null;
};

