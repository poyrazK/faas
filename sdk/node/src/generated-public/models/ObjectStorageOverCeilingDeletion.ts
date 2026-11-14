/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Present while the account stores more than its object-storage ceiling. Unless the account gets back under the ceiling first, Gregale deletes the excess on deletion_scheduled_at.
 */
export type ObjectStorageOverCeilingDeletion = {
  over_ceiling_since: string;
  deletion_scheduled_at: string;
  /**
   * Bytes stored above the ceiling at the last check.
   */
  excess_bytes: number;
};

