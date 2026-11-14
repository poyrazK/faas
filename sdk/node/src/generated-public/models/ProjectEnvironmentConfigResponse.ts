/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Latest immutable non-secret configuration snapshot for a project environment.
 */
export type ProjectEnvironmentConfigResponse = {
  project_slug: string;
  environment: string;
  version: number;
  config_hash: string;
  values: Record<string, any>;
  updated_at?: string;
};

