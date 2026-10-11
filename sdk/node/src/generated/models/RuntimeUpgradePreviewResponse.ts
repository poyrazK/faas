/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RuntimeReleaseResponse } from './RuntimeReleaseResponse.js';
/**
 * Component comparison and qualification requirements for a read-only runtime update plan.
 */
export type RuntimeUpgradePreviewResponse = {
  deployment_id: string;
  current: (RuntimeReleaseResponse | null);
  target: RuntimeReleaseResponse;
  disposition: 'blocked' | 'no_change' | 'review_required';
  changes: Array<'runtime_source' | 'guest_init' | 'base_layout' | 'base_bytes'>;
  blockers: Array<string>;
  required_steps: Array<string>;
  rebuild_required: boolean;
  cold_start_required: boolean;
  /**
   * Preview only; no runtime update operation is implemented.
   */
  execution_available: boolean;
};

