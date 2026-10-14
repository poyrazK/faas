/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Queue outcomes for one priority class.
 */
export type RoutePriorityQueueClass = {
  class: 'critical' | 'normal' | 'bulk';
  /**
   * Requests that waited for capacity.
   */
  queued: number;
  /**
   * Requests that took a lower class's queue place.
   */
  displacing: number;
  /**
   * Requests that lost their place to a higher class.
   */
  displaced: number;
  /**
   * Requests turned away because the queue was full.
   */
  rejected: number;
};

