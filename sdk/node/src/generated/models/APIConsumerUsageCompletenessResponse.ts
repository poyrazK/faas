/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Comparison of an API consumer's billing ledger with request telemetry over successful requests (ADR-941).
 */
export type APIConsumerUsageCompletenessResponse = {
  consumer_id: string;
  /**
   * gaps_detected means telemetry saw successful requests the ledger never billed; partial means telemetry confirms only part of the billed requests; unverifiable means telemetry holds no evidence for them.
   */
  status: 'verified' | 'partial' | 'gaps_detected' | 'unverifiable';
  /**
   * First whole UTC hour checked, clamped to request telemetry retention.
   */
  checked_from: string;
  /**
   * Exclusive end of the checked hours, clamped to hours that have settled.
   */
  checked_until: string;
  /**
   * Successful requests in the billing ledger.
   */
  ledger_requests: number;
  /**
   * Successful requests in request telemetry.
   */
  telemetry_requests: number;
  /**
   * Billed successful requests telemetry corroborates.
   */
  confirmed_requests: number;
  /**
   * Lower bound of successful requests telemetry saw that the ledger lacks.
   */
  missing_requests: number;
  hours_checked: number;
  /**
   * Hours with billed requests but no telemetry.
   */
  hours_without_telemetry: number;
};

