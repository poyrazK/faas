/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One crash snapshot (ADR-733). `status` moves from `requested` through
 * `capturing` to `ready`, then `expired`, or ends as `failed`. Storage
 * locations are never returned; open a ready capture as a fork.
 *
 */
export type CrashCaptureResponse = {
  id: string;
  app_id: string;
  deployment_id: string;
  trigger: 'http_5xx' | 'manual';
  status_code?: number;
  /**
   * Request path of the failing request (http_5xx only).
   */
  route?: string;
  status: 'requested' | 'capturing' | 'ready' | 'failed' | 'expired';
  mem_bytes?: number;
  failure?: {
    code: string;
    message: string;
  };
  requested_at: string;
  captured_at?: string;
  /**
   * When the capture is deleted.
   */
  expires_at?: string;
};

