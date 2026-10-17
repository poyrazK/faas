/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The outcome of an atomic deployment policy staging or cleanup operation.
 */
export type DeploymentStagingReceipt = {
  session_id: string;
  app_id: string;
  status: 'staged' | 'cleaned' | 'prepared' | 'activated' | 'conflict' | 'discarded';
  revision: number;
};

