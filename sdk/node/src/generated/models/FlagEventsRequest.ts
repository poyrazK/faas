/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagEvent } from './FlagEvent.js';
import type { FlagExposure } from './FlagExposure.js';
/**
 * A batch of runtime conversion events.
 */
export type FlagEventsRequest = {
  /**
   * 1 to 100 events, or none when the batch carries only exposures.
   */
  events?: Array<FlagEvent>;
  exposures?: Array<FlagExposure>;
};

