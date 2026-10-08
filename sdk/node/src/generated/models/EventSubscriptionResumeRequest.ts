/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Resume settings for a paused subscription, including the continuing routing admission rate.
 */
export type EventSubscriptionResumeRequest = {
  /**
   * Maximum new routing admissions in a one-second window; zero removes pacing. This limit continues to apply to new publications after the backlog drains.
   */
  rate_per_second?: number;
};

