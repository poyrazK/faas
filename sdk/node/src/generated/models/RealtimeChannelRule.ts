/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Grants a channel to a verified token without the authorize callback. The pattern is literal text with {claim} placeholders that must equal the token's claims and an optional trailing * matching any suffix.
 */
export type RealtimeChannelRule = {
  pattern: string;
  /**
   * Must be true: a rule grants read access.
   */
  read: boolean;
  /**
   * Also grants setting presence state and sending signals on the channel. Without it those go to the authorize callback with permission presence.
   */
  presence?: boolean;
};

