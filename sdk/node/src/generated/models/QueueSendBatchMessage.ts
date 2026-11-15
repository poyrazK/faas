/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One message of a batch send.
 */
export type QueueSendBatchMessage = {
  payload?: Record<string, any>;
  /**
   * Ordering key for this message: it is delivered after earlier messages with the same group_key, from this batch or any send, and before later ones. Preview: refused with queue_groups_unavailable until released to the account.
   */
  group_key?: string;
};

