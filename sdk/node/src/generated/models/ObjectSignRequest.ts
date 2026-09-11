/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact object operation to authorize for a short time.
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
   * PUT only.
   */
  cache_control?: string;
  /**
   * PUT only.
   */
  content_disposition?: string;
  /**
   * PUT only.
   */
  content_encoding?: string;
  /**
   * PUT only.
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
};

