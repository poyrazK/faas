/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentResponse } from './DeploymentResponse.js';
/**
 * Paginated deployment list with a `next_before` cursor for backward pagination.
 */
export type DeploymentListResponse = {
  items: Array<DeploymentResponse>;
  next_before?: string | null;
};

