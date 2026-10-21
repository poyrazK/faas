/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Runtime variable and logical service pinned to an exact target deployment. The guest checks its private HTTPS value and authorized routing; no URLs or credentials are saved.
 */
export type MigrationServiceConnectionCheck = {
  env_key: string;
  service: string;
  deployment_id: string;
};

