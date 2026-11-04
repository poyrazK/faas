/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Counts newly violated route and signal budgets since the prior incident evaluation.
 */
export type RouteMonitorWebhookEscalation = {
  previous_checked_at: string;
  newly_violated_routes: number;
  newly_violated_signals: number;
};

