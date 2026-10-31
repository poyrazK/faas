/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Outcome of a test notification.
 */
export type TestNotificationChannelResponse = {
  /**
   * Whether the destination accepted the test.
   */
  delivered: boolean;
  /**
   * Why delivery failed, when it did.
   */
  error?: string;
};

