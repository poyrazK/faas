/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PublishEventResponse } from './PublishEventResponse.js';
/**
 * Snapshot comparison of intended content with an existing retained publication.
 */
export type AppEventPublicationVerification = {
  app_id: string;
  source: string;
  event_id: string;
  observed_at: string;
  /**
   * Whether retained normalized type, schema version and JSON data match the supplied intent.
   */
  status: 'match' | 'conflict' | 'unavailable';
  /**
   * Verification could not observe a retained identity; nonpublication remains unproven.
   */
  reason?: 'not_retained_or_not_observed';
  receipt_url: string;
  receipt?: PublishEventResponse;
};

