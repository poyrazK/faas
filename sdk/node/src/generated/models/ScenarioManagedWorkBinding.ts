/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Platform identities and fixture resources bound to a logical workload. Missing identities make evidence incomplete.
 */
export type ScenarioManagedWorkBinding = {
  workload: string;
  app_id: string;
  deployment_id: string;
  postgres_resource_id?: string;
  objects_resource_id?: string;
};

