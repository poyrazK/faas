/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Rotate a deploy token. Empty label inherits the predecessor; empty expiry defaults to 90 days.
 */
export type RotateDeployTokenRequest = {
  label?: string;
  expires_at?: string;
};

