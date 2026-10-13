/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecipientBlockerQuery } from './EventRecipientBlockerQuery.js';
import type { EventRecipientBlockerResponse } from './EventRecipientBlockerResponse.js';
/**
 * Exactly one of result or error is returned.
 */
export type EventRecipientBlockerBatchItem = {
  query: EventRecipientBlockerQuery;
  result?: EventRecipientBlockerResponse;
  error?: 'read_error' | 'read_timeout';
};

