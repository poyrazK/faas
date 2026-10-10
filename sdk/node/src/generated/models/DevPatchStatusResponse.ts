/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Delivery state of one developer live patch (ADR-740).
 */
export type DevPatchStatusResponse = {
  generation: number;
  state: 'pending' | 'applied' | 'failed';
  created_at: string;
  /**
   * When the first instance acknowledged the patch.
   */
  applied_at?: string;
  /**
   * How long the instance took to write the patch and request the restart.
   */
  apply_ms?: number;
  /**
   * Bounded guest error code when state is failed, for example apply_failed or restart_failed.
   */
  error_code?: string;
};

