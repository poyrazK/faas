/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsWakeEvent } from './ObsWakeEvent.js';
/**
 * Every platform event recorded for one wake, oldest first.
 */
export type ObsWakeTimeline = {
  wake_id: string;
  /**
   * The first subject the events name (the app)
   */
  subject?: string;
  events: Array<ObsWakeEvent>;
  /**
   * True when the wake recorded more than 1000 events.
   */
  truncated: boolean;
};

