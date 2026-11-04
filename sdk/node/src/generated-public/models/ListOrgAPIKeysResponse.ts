/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APIKeyResponse } from './APIKeyResponse.js';
/**
 * GET /v1/orgs/{slug}/keys body. Returns every key minted against the org (active + grace + revoked). Newest first; matches the legacy `GET /v1/keys` ordering.
 */
export type ListOrgAPIKeysResponse = {
  keys: Array<APIKeyResponse>;
};

