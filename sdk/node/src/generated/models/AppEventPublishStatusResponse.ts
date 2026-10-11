/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReceiptResponse } from './EventReceiptResponse.js';
import type { PublishEventResponse } from './PublishEventResponse.js';
/**
 * Read-only producer-key reconciliation with retained consumer evidence.
 */
export type AppEventPublishStatusResponse = {
  /**
   * Saved acceptance timestamp requested for this status snapshot, emitted in UTC.
   */
  expected_accepted_at?: string;
  /**
   * Relationship of the retained receipt to the requested acceptance timestamp; absent for unguarded reads.
   */
  acceptance?: 'same_acceptance' | 'replacement_acceptance' | 'unavailable';
  app_id: string;
  source: string;
  event_id: string;
  observed_at: string;
  /**
   * Routing progress of retained acceptance; never a consumer success claim.
   */
  status: 'processing' | 'accepted' | 'unavailable';
  /**
   * Missing receipt observation cannot establish nonexecution.
   */
  reason?: 'not_retained_or_not_observed';
  receipt_url: string;
  receipt?: PublishEventResponse;
  evidence?: EventReceiptResponse;
};

