/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentEdgePolicyResponse } from './ProjectEnvironmentEdgePolicyResponse.js';
/**
 * Difference in environment headers/CORS rules or ownership.
 */
export type ProjectEnvironmentEdgePolicyDiffResponse = {
  kind: 'unchanged' | 'changed';
  before: ProjectEnvironmentEdgePolicyResponse;
  after: ProjectEnvironmentEdgePolicyResponse;
};

