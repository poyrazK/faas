/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Discovery progress; completed discovery does not imply admitted cleanup has completed.
 */
export type ObjectLifecycleScan = {
  id: string;
  bucket_id: string;
  revision: number;
  state: 'scanning' | 'completed' | 'cancelled';
  phase: 'objects' | 'multipart';
  scanned_keys: number;
  scanned_uploads: number;
  created_at: string;
  updated_at: string;
  finished_at?: string;
};

