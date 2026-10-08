/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedRealtimeReducerResponse = {
  channel: string;
  sequence: number;
  entities: Record<string, Record<string, any>>;
  /**
   * Scheduled entity deadlines; cleanup is asynchronous.
   */
  entity_expirations?: Record<string, string>;
  /**
   * Entity versions, including deletion tombstones.
   */
  entity_versions: Record<string, number>;
  updated_at: string;
};

