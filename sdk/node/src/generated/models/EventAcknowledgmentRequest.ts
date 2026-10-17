/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App-authorized callback fenced by the current opaque delivery token.
 */
export type EventAcknowledgmentRequest = {
  /**
   * Secret attempt token from the authenticated internal delivery; never log or persist in diagnostic artifacts.
   */
  token: string;
  /**
   * Optional JSON completion result up to one MiB; omitted means null and renewal forbids this field.
   */
  result?: any;
};

