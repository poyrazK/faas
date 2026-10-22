/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecipientBlockerQuery } from './EventRecipientBlockerQuery.js';
/**
 * Bounded exact-recipient queries, each guarded by the original acceptance instant.
 */
export type EventRecipientBlockerBatchRequest = {
  queries: Array<EventRecipientBlockerQuery>;
};

