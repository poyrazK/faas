/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConnectionBudgetRuntimeObservations } from './ConnectionBudgetRuntimeObservations.js';
/**
 * Canonical runtime pools and explicit planning headroom. Unbounded consumers remain unknown.
 */
export type ConnectionBudgetDemand = {
  data_api_resident_estimate: number;
  data_api_wake_estimate: number;
  auth_wake_estimate: number;
  control_headroom: number;
  migration_headroom: number;
  modeled_peak_connections: number;
  runtime_observations?: ConnectionBudgetRuntimeObservations;
};

