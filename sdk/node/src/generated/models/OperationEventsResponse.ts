/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationEvent } from './OperationEvent.js';
/**
 * One bounded event page, or instruction to read a fresh status snapshot.
 */
export type OperationEventsResponse = {
  events: Array<OperationEvent>;
  latest_sequence: number;
  resync_required: boolean;
};

