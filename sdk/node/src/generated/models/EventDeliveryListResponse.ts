/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventDeliveryResponse } from './EventDeliveryResponse.js';
/**
 * App-scoped event delivery page, ordered newest first.
 */
export type EventDeliveryListResponse = {
  app_slug: string;
  deliveries: Array<EventDeliveryResponse>;
  /**
   * ID cursor for the next older page.
   */
  next_before?: string;
};

