/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Problem } from './Problem.js';
import type { QueueSendResponse } from './QueueSendResponse.js';
/**
 * The outcome of one message by its zero-based input index: accepted, rejected with the problem, or not attempted after an earlier rejection.
 */
export type QueueSendBatchResult = {
  /**
   * Zero-based input position.
   */
  index: number;
  status: 'accepted' | 'rejected' | 'not_attempted';
  message?: QueueSendResponse;
  problem?: Problem;
};

