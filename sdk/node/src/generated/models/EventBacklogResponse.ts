/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventBacklogConsumer } from './EventBacklogConsumer.js';
import type { EventBacklogRecipient } from './EventBacklogRecipient.js';
/**
 * Live waiting recipients and exact matching counts, with an anchored acceptance window.
 */
export type EventBacklogResponse = {
  /**
   * Time of this live observation.
   */
  observed_at: string;
  /**
   * First-page time anchoring acceptance and minimum-age cutoff across pages.
   */
  window_at: string;
  coverage: string;
  recipients: Array<EventBacklogRecipient>;
  consumers: Array<EventBacklogConsumer>;
  /**
   * Account-wide unresolved receipts without a captured snapshot; only the age/acceptance window applies.
   */
  unattributed_receipts: number;
  /**
   * Opaque continuation for the recipient page.
   */
  next_after?: string;
  /**
   * Opaque continuation for the consumer page.
   */
  next_consumers_after?: string;
};

