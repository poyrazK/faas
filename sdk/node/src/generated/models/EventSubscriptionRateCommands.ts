/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionRateCommandReceipt } from './EventSubscriptionRateCommandReceipt.js';
/**
 * Bounded retained rate command history in increasing sequence order; expiry can leave sequence gaps. Pages are fresh observations.
 */
export type EventSubscriptionRateCommands = {
  receipts: Array<EventSubscriptionRateCommandReceipt>;
  /**
   * Exclusive cursor for the next page when another retained receipt exists.
   */
  next_after?: number;
};

