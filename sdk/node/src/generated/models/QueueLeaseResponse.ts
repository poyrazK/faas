/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { QueueLeaseMessage } from './QueueLeaseMessage.js';
/**
 * Leased messages; empty when none were ready before the wait ended.
 */
export type QueueLeaseResponse = {
  messages: Array<QueueLeaseMessage>;
};

