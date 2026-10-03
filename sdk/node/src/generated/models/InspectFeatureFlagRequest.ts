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
   * Opaque application subject ID after authentication; requires customer_id and does not record the ID in evidence.
   */
  subject_id?: string;
  /**
   * Zero or omitted selects current configuration.
   */
  version?: number;
  fallback?: boolean;
  /**
   * Fallback variant for an absent flag; configured variant flags use their own default.
   */
  fallback_variant?: string;
};

