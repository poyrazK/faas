/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * 200 — a dequeued row (the long-poll hit). 204 (no body) on timeout.
 */
export type QueueReceiveResponse = {
  id: string;
  payload: Record<string, any>;
  result?: Record<string, any>;
};

