/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable acceptance receipt for a published internal event.
 */
export type PublishEventResponse = {
  id: string;
  accepted_at: string;
  account_id: string;
  /**
   * Account-authenticated relative URL for this event receipt.
   */
  receipt_url: string;
};

