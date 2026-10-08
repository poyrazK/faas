/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSchemaRolloutConsumer } from './EventSchemaRolloutConsumer.js';
import type { EventSchemaRolloutRetained } from './EventSchemaRolloutRetained.js';
import type { EventSchemaRolloutValidation } from './EventSchemaRolloutValidation.js';
export type EventSchemaRolloutResponse = {
  source: string;
  type: string;
  version: string;
  schema_origin: 'proposed' | 'registered';
  /**
   * SHA-256 of the schema bytes checked.
   */
  schema_digest: string;
  observed_at: string;
  /**
   * Observed matching enabled application subscriptions; a lower bound when truncated.
   */
  consumer_count: number;
  accepting_count: number;
  excluding_count: number;
  consumers_truncated: boolean;
  consumers: Array<EventSchemaRolloutConsumer>;
  sample_valid_count: number;
  sample_invalid_count: number;
  samples: Array<EventSchemaRolloutValidation>;
  retained: EventSchemaRolloutRetained;
};

