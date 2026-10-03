/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentRoutePolicyResponse } from './ProjectEnvironmentRoutePolicyResponse.js';
/**
 * Difference in the effective declared-route contract or its ownership.
 */
export type ProjectEnvironmentRoutePolicyDiffResponse = {
  kind: 'unchanged' | 'changed';
  before: ProjectEnvironmentRoutePolicyResponse;
  after: ProjectEnvironmentRoutePolicyResponse;
};

