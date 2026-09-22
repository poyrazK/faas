/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One sanitized historical request and its optional expected source-response metadata.
 */
export type MirrorReplayRequestItem = {
  /**
   * Optional correlation id; generated when omitted.
   */
  request_id?: string;
  method: 'GET' | 'HEAD' | 'OPTIONS' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  /**
   * Absolute request path with optional query; absolute URLs are rejected.
   */
  path: string;
  headers?: Record<string, string>;
  /**
   * Sanitized JSON request body, bounded to 64 KiB.
   */
  body?: any;
  expected_status?: number;
  expected_latency_ms?: number;
  /**
   * Optional SHA-256 of the historical source response body. The body itself is not uploaded.
   */
  expected_body_sha256?: string;
};

