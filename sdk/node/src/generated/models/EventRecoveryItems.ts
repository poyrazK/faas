/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryItem } from './EventRecoveryItem.js';
/**
 * Paginated frozen recovery items and their individual admission outcomes.
 */
export type EventRecoveryItems = {
  job_id: string;
  items: Array<EventRecoveryItem>;
  /**
   * Last item position for the next page; absent on the final page.
   */
  next_after?: number;
};

