/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Initial entity state and sequence for the channel reducer.
 */
export type ManagedRealtimeReducerRequest = {
  sequence: number;
  /**
   * Seed entities, at most 64 KiB encoded. Keys are 1..128 UTF-8 bytes.
   */
  entities: Record<string, Record<string, any>>;
};

