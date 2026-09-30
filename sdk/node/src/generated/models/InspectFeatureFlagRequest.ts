/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-authorized simulation context; does not record exposure.
 */
export type InspectFeatureFlagRequest = {
  /**
   * Omit for anonymous evaluation.
   */
  customer_id?: string;
  /**
   * Zero or omitted selects current configuration.
   */
  version?: number;
  fallback?: boolean;
};

