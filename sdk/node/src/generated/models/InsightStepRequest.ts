/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A funnel or retention step: an event and property values its events must carry.
 */
export type InsightStepRequest = {
  event: string;
  /**
   * Property values the step's events must carry.
   */
  where?: Record<string, string>;
};

