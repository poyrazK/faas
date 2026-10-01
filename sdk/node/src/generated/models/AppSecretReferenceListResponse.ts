/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current environment identity, destination-to-source names and shared variable/reference quota usage.
 */
export type AppSecretReferenceListResponse = {
  environment_id: string;
  environment: string;
  references: Record<string, string>;
  /**
   * Shared variable/reference quota usage across all environments.
   */
  count: number;
  quota: number;
};

