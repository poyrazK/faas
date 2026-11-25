/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounds for automatic increases to the database volume.
 */
export type SetManagedPostgresAutoGrowRequest = {
  /**
   * Per-volume ceiling in bytes. Zero disables automatic growth.
   */
  max_bytes: number;
};

