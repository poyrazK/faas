/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Last observed filesystem capacity and pressure for a native database volume.
 */
export type ManagedPostgresVolumeStatus = {
  total_bytes: number;
  free_bytes: number;
  /**
   * Whether free space was below the storage guard recovery threshold.
   */
  low: boolean;
  /**
   * Whether the primary storage guard was in read-only mode.
   */
  read_only: boolean;
  sampled_at: string;
  /**
   * Whether the observation is recent and belongs to the current running placement.
   */
  fresh: boolean;
};

