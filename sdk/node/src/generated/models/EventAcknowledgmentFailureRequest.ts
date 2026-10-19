/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App-authorized negative acknowledgment for the current live attempt; repeat exactly after an uncertain response.
 */
export type EventAcknowledgmentFailureRequest = {
  /**
   * Secret attempt token; never log it or put it in a URL.
   */
  token: string;
  /**
   * Non-sensitive failure code; no payloads or credentials.
   */
  reason: string;
};

