/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventConsumerCapacityWaits } from './EventConsumerCapacityWaits.js';
import type { EventConsumerExecutionHealth } from './EventConsumerExecutionHealth.js';
import type { EventConsumerHealth } from './EventConsumerHealth.js';
import type { EventConsumerPendingSample } from './EventConsumerPendingSample.js';
/**
 * Independent live routing, retained execution, and oldest-first pending reads. No overall healthy verdict is inferred from incomplete coverage.
 */
export type EventConsumerDeliveryHealth = {
  capacity_waits?: EventConsumerCapacityWaits;
  started_at: string;
  observed_at: string;
  routing: EventConsumerHealth;
  execution: EventConsumerExecutionHealth;
  pending_sample: EventConsumerPendingSample;
};

