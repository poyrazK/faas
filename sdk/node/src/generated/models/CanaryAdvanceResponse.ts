/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentResponse } from './DeploymentResponse.js';
/**
 * The atomic canary transition result and the deployment_audit row id.
 */
export type CanaryAdvanceResponse = {
  deployment: DeploymentResponse;
  /**
   * The deployment_audit row id, stringified for SDK portability.
   */
  audit_id: string;
};
