/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationDeliveryAttempt } from './OperationDeliveryAttempt.js';
import type { OperationEvent } from './OperationEvent.js';
import type { OperationTimelineAttempt } from './OperationTimelineAttempt.js';
/**
 * One timeline record; kind selects which of event, attempt or delivery is present.
 */
export type OperationTimelineEntry = {
  at: string;
  kind: 'event' | 'attempt' | 'step_attempt' | 'delivery_attempt';
  event?: OperationEvent;
  attempt?: OperationTimelineAttempt;
  delivery?: OperationDeliveryAttempt;
};

