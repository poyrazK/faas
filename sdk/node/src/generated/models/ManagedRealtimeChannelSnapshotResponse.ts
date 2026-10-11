/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Channel state snapshot with sequence and entity expiration metadata.
 */
export type ManagedRealtimeChannelSnapshotResponse = {
  channel: string;
  sequence: number;
  resume_after_sequence: number;
  data_base64: string;
  binary: boolean;
  /**
   * Entity cleanup deadlines carried by this channel snapshot; cleanup runs asynchronously.
   */
  entity_expirations?: Record<string, string>;
  /**
   * Entity versions, including deletion tombstones; reducer snapshots only.
   */
  entity_versions?: Record<string, number>;
  updated_at: string;
  expires_at: string;
};

