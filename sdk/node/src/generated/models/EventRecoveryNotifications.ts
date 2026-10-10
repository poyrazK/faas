/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotification } from './EventRecoveryNotification.js';
/**
 * Metadata-only recovery notification report. A captured event does not imply acknowledgement. Reads do not capture events, relay the outbox or retry deliveries. Missing or pruned receiver evidence remains unknown.
 */
export type EventRecoveryNotifications = {
  job_id: string;
  app_id: string;
  app_slug: string;
  observed_at: string;
  receiver_limit: 100;
  notifications: Array<EventRecoveryNotification>;
};

