/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeadLetterEvent } from './DeadLetterEvent.js';
/**
 * A page of account-wide unified dead-letter events ordered newest-first.
 */
export type AccountDeadLetterEventsResponse = {
  events: Array<DeadLetterEvent>;
  next_before?: string;
};

