/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentEdgeRuleResponse } from './ProjectEnvironmentEdgeRuleResponse.js';
/**
 * Headers/CORS policy ownership and rules. Other edge-rule kinds remain application-owned.
 */
export type ProjectEnvironmentEdgePolicyResponse = {
  ownership: 'application' | 'environment';
  rules: Array<ProjectEnvironmentEdgeRuleResponse>;
};

