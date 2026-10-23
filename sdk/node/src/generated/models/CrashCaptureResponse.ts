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
  /**
   * `http_5xx` after a 5xx response, `manual` from this API, `sdk` from the
   * app itself through the guest metadata endpoint
   * (`POST http://169.254.169.254/v1/crash-snapshots:capture`), `live_fork`
   * for the capture a live fork took (kept only as long as a fork can live).
   *
   */
  trigger: 'http_5xx' | 'manual' | 'sdk' | 'live_fork';
  status_code?: number;
  /**
   * Request path of the failing request (http_5xx
   */
  route?: string;
  /**
   * The app's label for an sdk capture.
   */
  reason?: string;
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

