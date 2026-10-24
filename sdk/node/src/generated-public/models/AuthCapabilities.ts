/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AuthProviders } from './AuthProviders.js';
/**
 * Boot-resolved OAuth provider state for this apid host.
 * `enabled=true` means the consent route (`/v1/auth/<name>`)
 * will issue a 302 to the upstream consent screen;
 * `enabled=false` means the consent route returns 503
 * `oauth_provider_unavailable` because both the provider's
 * `*_CLIENT_ID` and `*_CLIENT_SECRET` were unset at boot.
 *
 */
export type AuthCapabilities = {
  providers: AuthProviders;
};

