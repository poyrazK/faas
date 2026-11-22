/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectEncryption } from './ObjectEncryption.js';
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
  state: 'initiating' | 'active' | 'completing' | 'completing_conditional' | 'aborting' | 'completed' | 'aborted';
  /**
   * Persisted conditional completion rejection; retries retain the outcome.
   */
  completion_error_code?: string;
  /**
   * Actual committed ETag when completion is confirmed.
   */
  etag?: string;
  /**
   * Owned public version ID when completion is confirmed; null denotes a mutable provider version.
   */
  version_id?: string;
  encryption?: ObjectEncryption;
  expires_at: string;
  created_at: string;
};

