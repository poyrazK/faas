/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request to enqueue a signed webhook delivery owned by the source app.
 */
export type DeliverAppEventRequest = {
  /**
   * A webhook id or exact registered target URL owned by the source app.
   */
  destination: string;
  type: string;
  /**
   * Any valid JSON value placed in the signed webhook envelope.
   */
  data: any;
};

