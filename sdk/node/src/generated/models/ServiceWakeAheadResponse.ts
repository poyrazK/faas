/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An app's service wake-ahead opt-in (ADR-956).
 */
export type ServiceWakeAheadResponse = {
  slug: string;
  /**
   * Whether a cold wake of this app also wakes the services it is measured to call.
   */
  enabled: boolean;
  /**
   * Omitted until the opt-in is first changed.
   */
  updated_at?: string;
};

