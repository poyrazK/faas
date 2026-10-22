/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fenced request to execute one saved case input against a specific published revision and deployment.
 */
export type StartAutomationLiveVerificationRequest = {
  expected_version: number;
  expected_published_version: number;
  expected_deployment_id: string;
  case_name: string;
  /**
   * Must be true to authorize real handler and connected-service actions.
   */
  confirm_real_actions: boolean;
};

