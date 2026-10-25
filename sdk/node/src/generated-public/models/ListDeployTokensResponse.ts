/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeployTokenResponse } from './DeployTokenResponse.js';
/**
 * GET /v1/apps/{slug}/deploy-tokens response. Plaintexts are never returned by list.
 */
export type ListDeployTokensResponse = {
  tokens: Array<DeployTokenResponse>;
};

