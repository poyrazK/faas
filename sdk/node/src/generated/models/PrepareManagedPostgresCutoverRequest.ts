/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stage source bindings for one app and scope on a ready database restored from that source.
 */
export type PrepareManagedPostgresCutoverRequest = {
  source_database_id: string;
  target_database_id: string;
  app_id: string;
  scope: string;
};

