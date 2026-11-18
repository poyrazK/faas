/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An env var envelope scoped to one named environment. The plaintext value appears only on a `?values=true` listing.
 */
export type ScopedAppEnvResponse = {
  scope: string;
  key: string;
  created_at: string;
  updated_at: string;
  /**
   * This scope's plaintext value of the variable, included only on a `?values=true` listing.
   */
  value?: string;
};

