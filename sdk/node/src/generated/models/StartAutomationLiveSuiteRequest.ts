/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * At most 16 KiB. Bind an ordered selection of saved live case names to the exact saved version, published revision and deployment, with explicit real-action authorization.
 */
export type StartAutomationLiveSuiteRequest = {
  expected_version: number;
  expected_published_version: number;
  expected_deployment_id: string;
  cases: Array<string>;
  confirm_real_actions: boolean;
};

