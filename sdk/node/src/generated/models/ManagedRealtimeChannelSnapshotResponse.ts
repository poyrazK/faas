/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedRealtimeChannelSnapshotResponse = {
  channel: string;
  sequence: number;
  resume_after_sequence: number;
  data_base64: string;
  binary: boolean;
  /**
   * Scheduled entity deadlines; cleanup is asynchronous.
   */
  entity_expirations?: Record<string, string>;
  /**
   * Entity versions, including deletion tombstones; reducer snapshots only.
   */
  entity_versions?: Record<string, number>;
  updated_at: string;
  expires_at: string;
};

