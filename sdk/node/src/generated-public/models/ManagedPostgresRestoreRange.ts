/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The span a point-in-time restore from this database can target now. Absent while the database cannot be restored from.
 */
export type ManagedPostgresRestoreRange = {
  /**
   * The later of the database's creation and the start of its restore window.
   */
  earliest: string;
  /**
   * When the response was made; a restore point must be before it.
   */
  latest: string;
};

