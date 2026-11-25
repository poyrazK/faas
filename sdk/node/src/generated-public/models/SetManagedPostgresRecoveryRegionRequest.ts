/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Destination region for the database's recovery archive.
 */
export type SetManagedPostgresRecoveryRegionRequest = {
  /**
   * The configured recovery destination. Send an empty string to disable regional recovery.
   */
  region: string;
};

