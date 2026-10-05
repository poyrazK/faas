/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Enrolled bucket configuration, default and per-version event holds, fixed version retention and legal-hold capabilities without a native health check.
 */
export type ObjectLockCapabilities = {
  bucket_configuration: boolean;
  default_event_hold: boolean;
  /**
   * Separately enrolled durable per-version event hold mutations.
   */
  version_event_hold: boolean;
  version_retention: boolean;
  version_legal_hold: boolean;
};

