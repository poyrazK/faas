/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventBacklogRecipient } from './EventBacklogRecipient.js';
/**
 * Up to 50 oldest pending application recipients, ordered by acceptance time with stable outbox and subscription ties. Waiting reason counts cover only these recipients.
 */
export type EventConsumerPendingSample = {
  observed_at: string;
  coverage: 'oldest_pending_recipients';
  /**
   * More pending recipients existed in this read.
   */
  truncated: boolean;
  waiting_reasons: Record<string, number>;
  recipients: Array<EventBacklogRecipient>;
};

