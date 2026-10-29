/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One captured workflow recipient; event IDs must be distinct. Generation is required for independent recipient routing and omitted for legacy receipts. Replay count also fences legacy recovery cycles. No delivery-age override is supported.
 */
export type EventWorkflowRecoveryTarget = {
  event_id: string;
  subscription_id: string;
  replay_count: number;
  generation?: number;
};

