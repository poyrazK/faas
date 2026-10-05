/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Specify exactly one of app or project. App fields use variables/KEY or source; project fields use configuration/KEY. Terraform must claim before writes and release after deletion. Claims contain no values.
 */
export type EnvironmentFieldOwnershipRequest = {
  app?: string;
  project?: string;
  environment: string;
  paths: Array<string>;
  manager: 'terraform';
};

