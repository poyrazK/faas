/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current environment identity, destination-to-source names, suppressed destination keys and shared variable/reference quota usage.
 */
export type AppSecretReferenceListResponse = {
  environment_id: string;
  environment: string;
  references: Record<string, string>;
  /**
   * Destinations excluded from primary workload secret delivery until an explicit PUT reference re-enables them. Omitted by older servers; consumes no variable slots.
   */
  suppressed_keys?: Array<string>;
  /**
   * Shared variable/reference quota usage across all environments.
   */
  count: number;
  quota: number;
};

