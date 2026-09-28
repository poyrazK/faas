/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventDeliveryResponse } from './EventDeliveryResponse.js';
import type { EventFanoutFailureResponse } from './EventFanoutFailureResponse.js';
/**
 * App-scoped event invocation and fanout failure history, each ordered newest first.
 */
export type EventDeliveryListResponse = {
  app_slug: string;
  deliveries: Array<EventDeliveryResponse>;
  /**
   * ID cursor for the next older page.
   */
  next_before?: string;
  /**
   * Terminal recipient routing failures; empty when none exist.
   */
  fanout_failures?: Array<EventFanoutFailureResponse>;
  /**
   * Opaque cursor for the next older page of pre-invocation failures.
   */
  next_fanout_before?: string;
};

