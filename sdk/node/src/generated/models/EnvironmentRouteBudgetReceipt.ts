/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original durable policy-intent receipt. Request fingerprints bind exact bodies but grant no write authority. No configuration values are exposed.
 */
export type EnvironmentRouteBudgetReceipt = {
  request_id: string;
  request_hash: string;
  app_id: string;
  scope: string;
  project_id?: string;
  environment_id?: string;
  revision: number;
  app_revision: number;
  budget_source: 'app_policy' | 'environment_policy';
};

