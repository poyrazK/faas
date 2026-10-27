/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeployTokenResponse } from './DeployTokenResponse.js';
/**
 * Replacement deploy-token metadata and one-time plaintext, plus the revoked predecessor id.
 */
export type RotateDeployTokenResponse = {
  token: DeployTokenResponse;
  token_plaintext: string;
  old_token_id: string;
  old_token_expires_at?: string;
};

