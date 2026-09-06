/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable provider-neutral resumable upload session. The provider upload ID is private.
 */
export type ObjectMultipartUpload = {
  id: string;
  key: string;
  size_bytes: number;
  part_size_bytes: number;
  part_count: number;
  content_type: string;
  state: 'initiating' | 'active' | 'completing' | 'aborting' | 'completed' | 'aborted';
  expires_at: string;
  created_at: string;
};

