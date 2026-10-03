/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorCustomerImpact } from './RouteMonitorCustomerImpact.js';
/**
 * Transition metadata and optional aggregate customer-impact counts with an authenticated saved incident path. Excludes customer IDs and request data.
 */
export type RouteMonitorWebhookPayload = {
  version: number;
  app_id: string;
  deployment_id: string;
  incident_id: string;
  revision: number;
  status: 'open' | 'recovered';
  checked_at: string;
  incident_path: string;
  customer_impact?: RouteMonitorCustomerImpact;
};

