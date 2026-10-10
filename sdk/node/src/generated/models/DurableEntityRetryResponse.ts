/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Acknowledged recovery metadata reset for one exhausted alarm or outgoing message.
 */
export type DurableEntityRetryResponse = {
  version: number;
  target: 'alarm' | 'outbox';
  /**
   * Retry metadata was reset; no execution or receiver completion is implied.
   */
  rearmed: boolean;
};

