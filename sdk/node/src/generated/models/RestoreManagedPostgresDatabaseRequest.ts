/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Point-in-time restore request that creates a new database. A restore is a copy-on-write branch of the source where the backend supports it.
 */
export type RestoreManagedPostgresDatabaseRequest = {
  name: string;
  /**
   * Recover from the archive in the configured recovery region.
   */
  region?: string;
  /**
   * A time inside the source's restore_range. Omit it to use the latest point. A regional restore uses the latest point verified in the recovery archive.
   */
  point_in_time?: string;
  /**
   * Purge the new database this many seconds after it is created. Its bindings are revoked first; the source is never touched. The database shows expires_at.
   */
  ttl_seconds?: number;
};

