/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Dead Letter Export Manifest.
 */
export type RealtimeDeadLetterExportManifest = {
  type: 'manifest';
  format_version: 1;
  group_id: string;
  group: string;
  channel: string;
  exported_at: string;
  snapshot_at: string;
  after_sequence: string;
  through_sequence: string;
  failure_codes: Array<string>;
  count: number;
  has_more: boolean;
  next_after_sequence: string;
};

