/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentEdgeRuleResponse } from './ProjectEnvironmentEdgeRuleResponse.js';
/**
 * Complete replacement for an environment's headers/CORS rules. Inline CORS settings are required; shared presets are not accepted.
 */
export type UpdateProjectEnvironmentEdgePolicyRequest = {
  rules: Array<ProjectEnvironmentEdgeRuleResponse>;
};

