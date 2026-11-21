/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionEnvironmentEvent } from './ExecutionEnvironmentEvent.js';
/**
 * An ordered page of retained dependency environment build records.
 */
export type ExecutionEnvironmentEventsResponse = {
  events: Array<ExecutionEnvironmentEvent>;
  next_sequence: number;
  gap?: boolean;
  status: string;
};

