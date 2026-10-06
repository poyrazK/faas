/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReplayEventFanoutFailureRequest } from './ReplayEventFanoutFailureRequest.js';
/**
 * Selective recovery request through an existing authorized endpoint. GET receipt inspection never performs this action.
 */
export type EventReceiptRecoveryAction = {
  kind: 'routing_replay' | 'handler_replay' | 'keyed_handler_replay' | 'dead_letter_replay';
  method: 'POST';
  url: string;
  body?: ReplayEventFanoutFailureRequest;
};

