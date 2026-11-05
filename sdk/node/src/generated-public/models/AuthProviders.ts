/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OAuthProviderCapability } from './OAuthProviderCapability.js';
/**
 * Per-provider capability map. Closed set (google, github) —
 * handlers MUST add a new field here when adding a new
 * provider, not relax this to a free-form map.
 *
 */
export type AuthProviders = {
  google: OAuthProviderCapability;
  github: OAuthProviderCapability;
};

