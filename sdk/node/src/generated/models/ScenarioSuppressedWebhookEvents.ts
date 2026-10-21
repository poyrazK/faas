/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Count of ordinary webhook deliveries the capture fence suppressed for one workload and event type. Their payloads carry platform-generated identifiers, so only multiplicity compares.
 */
export type ScenarioSuppressedWebhookEvents = {
  workload: string;
  event: string;
  count: number;
};

