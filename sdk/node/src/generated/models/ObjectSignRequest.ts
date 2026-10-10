/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectEncryption } from './ObjectEncryption.js';
import type { ObjectWriteProtection } from './ObjectWriteProtection.js';
/**
 * Exact object operation to authorize at the branded S3 gateway. PUT binds one durable receipt and supports an explicit owned encryption selection.
 */
export type ObjectSignRequest = {
  method: 'GET' | 'HEAD' | 'PUT';
  key: string;
  /**
   * GET/HEAD only. Exact owned immutable public version UUID. Mutable null and native provider selectors are rejected. Omit for the current object.
   */
  version_id?: string;
  /**
   * PUT only. Mutually exclusive with if_none_match. GCS accepts one strong quoted XML ETag or * and binds the observed content generation; a concurrent replacement is rejected even if it has the same ETag. Conditions are fixed in the signed capability.
   */
  if_match?: string;
  /**
   * PUT only. Create only if no live object exists. Mutually exclusive with if_match.
   */
  if_none_match?: '*';
  expires_in?: number;
  /**
   * Required for PUT; forbidden for GET.
   */
  size_bytes?: number;
  /**
   * PUT only.
   */
  content_type?: string;
  /**
   * Cache-Control value for PUT.
   */
  cache_control?: string;
  /**
   * Content-Disposition value for PUT.
   */
  content_disposition?: string;
  /**
   * Content-Encoding value for PUT.
   */
  content_encoding?: string;
  /**
   * Content-Language value for PUT.
   */
  content_language?: string;
  /**
   * PUT-only x-amz-meta-* values.
   */
  metadata?: Record<string, string>;
  /**
   * PUT-only S3 object tags.
   */
  tags?: Record<string, string>;
  protection?: ObjectWriteProtection;
  encryption?: ObjectEncryption;
};

