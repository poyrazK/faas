/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Encoded channel state representing an explicit committed sequence.
 */
export type ManagedRealtimeChannelSnapshotRequest = {
  sequence: number;
  data_base64: string;
  binary?: boolean;
};

