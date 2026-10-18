/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionCapacityCommandReceipt } from './EventSubscriptionCapacityCommandReceipt.js';
/**
 * One fresh page of retained original command evidence. History order does not establish execution order or current configuration.
 */
export type EventSubscriptionCapacityCommands = {
  /**
   * Owned application identifier.
   */
  app_id: string;
  /**
   * Owned subscription or retained control identifier.
   */
  subscription_id: string;
  /**
   * Normalized original-outcome filter for this page.
   */
  outcome: 'any' | 'applied' | 'unchanged';
  /**
   * Logical observation time shared by this page.
   */
  observed_at: string;
  /**
   * Matching unexpired original receipts in descending recorded_at and command UUID order. An accessible empty history returns an empty array.
   */
  receipts: Array<EventSubscriptionCapacityCommandReceipt>;
  /**
   * Whether another matching retained receipt existed at this observation.
   */
  has_more: boolean;
  /**
   * Opaque scope-bound boundary for the next page. Present only when has_more is true.
   */
  next_after?: string;
};

