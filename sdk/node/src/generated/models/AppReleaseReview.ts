/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type AppReleaseReview = {
  scope?: string;
  project_id?: string;
  environment_id?: string;
  workload_revision?: number;
  workload_config_hash?: string;
  protected?: boolean;
  app_id: string;
  configuration_hash: string;
  latest_deployment_id: string;
  serving_deployment_ids: Array<string>;
};

