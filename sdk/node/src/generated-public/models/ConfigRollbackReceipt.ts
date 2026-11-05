/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ConfigRollbackReceipt = {
  request_id: string;
  app_id: string;
  scope: string;
  environment_id?: string;
  restored_revision: number;
  config_revision: number;
  changed: boolean;
};

