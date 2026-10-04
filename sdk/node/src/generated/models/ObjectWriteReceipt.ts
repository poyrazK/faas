/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectEncryption } from './ObjectEncryption.js';
/**
 * Public receipt of one tracked PUT, CopyObject or application upload. Contains no credentials, provider placement, source identity or recovery lease tokens.
 */
export type ObjectWriteReceipt = {
  id: string;
  bucket_id: string;
  key: string;
  operation: 'put' | 'copy' | 'upload';
  bytes: number;
  content_type: string;
  /**
   * Confirmed ETag; empty until completion.
   */
  etag: string;
  status: 'pending' | 'completed' | 'failed';
  error_code?: 'preparation_expired' | 'usage_unavailable' | 'dispatch_failed' | 'provider_write_rejected' | 'provider_write_uncertain' | 'configuration';
  created_at: string;
  encryption?: ObjectEncryption;
};

