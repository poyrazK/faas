/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileRequestMixWindow } from './ProfileRequestMixWindow.js';
/**
 * Frozen observed telemetry summary, at most 16 KiB JSON. Completeness describes route/status aggregation, not telemetry delivery. Readable with the assessment after raw request telemetry expires.
 */
export type ProfileRequestMixSnapshot = {
  captured_at: string;
  status: 'captured' | 'partial' | 'unavailable';
  complete: boolean;
  reason: string;
  baseline?: ProfileRequestMixWindow;
  candidate?: ProfileRequestMixWindow;
  warnings: Array<string>;
  /**
   * Frozen total variation distance in percentage points; omitted for truncated or unavailable route distributions.
   */
  route_difference?: number;
  status_difference?: number;
};

