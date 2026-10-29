/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One measured caller-to-service edge and its wake-ahead outcomes.
 */
export type ServiceWakeAheadTarget = {
  app_id: string;
  slug: string;
  /**
   * Share of cold wakes followed by a call to this service.
   */
  call_share: number;
  /**
   * Share of those calls that found it parked or woken ahead.
   */
  cold_share: number;
  /**
   * The edge meets the wake-ahead thresholds.
   */
  qualifies: boolean;
  started: number;
  used: number;
  unused: number;
  failed: number;
};

