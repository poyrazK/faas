/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AgentActivityEntry } from './AgentActivityEntry.js';
/**
 * One page of an agent's write requests, newest first.
 */
export type AgentActivityResponse = {
  activity: Array<AgentActivityEntry>;
  /**
   * Pass as `before` to read the next older page; absent on the last page.
   */
  next_before?: string;
};

