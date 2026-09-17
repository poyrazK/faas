/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeadLetterEvent } from './DeadLetterEvent.js';
/**
 * A page of unified dead-letter events ordered newest-first.
 */
export type DeadLetterEventsResponse = {
  app_slug: string;
  events: Array<DeadLetterEvent>;
  next_before?: string;
};

