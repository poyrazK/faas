/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One database's share of the month's compute, with its recorded starts and awake time this month.
 */
export type ManagedPostgresDatabaseUsage = {
  database_id: string;
  /**
   * Empty for a database deleted since.
   */
  name: string;
  compute_unit_seconds: number;
  starts: number;
  awake_seconds: number;
};

