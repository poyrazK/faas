/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionResponse } from './EventSubscriptionResponse.js';
/**
 * App-scoped event subscriptions reconciled from the manifest.
 */
export type EventSubscriptionListResponse = {
  app_slug: string;
  subscriptions: Array<EventSubscriptionResponse>;
};

