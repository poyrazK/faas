/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Canonical UUID request for an in-place upgrade of an existing managed PostgreSQL database to a later major.
 */
export type UpgradeManagedPostgresDatabaseRequest = {
  /**
   * Canonical nonzero UUID for this upgrade; replay with the same database and major after uncertain responses.
   */
  request_id: string;
  /**
   * Target major; later than the database's and offered by its backend.
   */
  postgres_major: number;
};

