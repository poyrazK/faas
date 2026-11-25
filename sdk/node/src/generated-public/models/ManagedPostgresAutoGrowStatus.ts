/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedPostgresAutoGrowStatus = {
  max_bytes: number;
  proposed_bytes?: number;
  /**
   * Current policy or resize outcome code.
   */
  reason: string;
  resize_id?: string;
  last_grown_at?: string;
};

