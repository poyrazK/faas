/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleResponse } from './EdgeRuleResponse.js';
/**
 * Immutable gateway edge-rule state recorded on the deployment's first live transition.
 */
export type DeploymentRoutePolicySnapshotResponse = {
  deployment_id: string;
  app_id: string;
  scope: string;
  sha256: string;
  schema_version: number;
  captured_at: string;
  rules: Array<EdgeRuleResponse>;
};

