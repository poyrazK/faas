/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable storage enrollment acknowledgement containing neither content nor credentials.
 */
export type ScenarioObjectsReceipt = {
  /**
   * Private listener incarnation that acknowledged the namespace before deployment.
   */
  collector_id: string;
  version: 1;
  run_id: string;
  app_id: string;
  resource_id: string;
  fixture_sha256: string;
  contract_sha256: string;
  initial_sha256: string;
};

