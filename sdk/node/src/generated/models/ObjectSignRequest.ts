/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectEncryption } from './ObjectEncryption.js';
/**
 * Exact object operation to authorize at the branded S3 gateway. PUT binds one durable receipt and supports an explicit owned encryption selection.
 */
export type ObjectSignRequest = {
  method: 'GET' | 'HEAD' | 'PUT';
  key: string;
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
  encryption?: ObjectEncryption;
};

