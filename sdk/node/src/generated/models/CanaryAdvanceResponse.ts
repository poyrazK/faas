/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentResponse } from './DeploymentResponse.js';
import type { RouteGateDecision } from './RouteGateDecision.js';
import type { RouteHealthDecision } from './RouteHealthDecision.js';
/**
 * The atomic canary transition result and the deployment_audit row id.
 */
export type CanaryAdvanceResponse = {
  route_health?: RouteHealthDecision;
  route_gate?: RouteGateDecision;
  deployment: DeploymentResponse;
  /**
   * The deployment_audit row id, stringified for SDK portability.
   */
  audit_id: string;
};

