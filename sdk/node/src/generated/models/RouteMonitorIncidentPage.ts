/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorIncident } from './RouteMonitorIncident.js';
/**
 * Bounded owned incident history ordered by opening time and UUID descending.
 */
export type RouteMonitorIncidentPage = {
  app_id: string;
  incidents: Array<RouteMonitorIncident>;
  next_before?: string;
};

