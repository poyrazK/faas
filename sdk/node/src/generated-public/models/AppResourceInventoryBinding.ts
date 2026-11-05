/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Selected-app database linkage without provider identities or credential references.
 */
export type AppResourceInventoryBinding = {
  id: string;
  database_id: string;
  app_id: string;
  scope: string;
  access: string;
  state: string;
  environment_key: string;
  rotation_pending: boolean;
};

