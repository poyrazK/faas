/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeclaredRoute } from './DeclaredRoute.js';
/**
 * Review-time snapshot of the scoped routes, consumers, schedule and accepted work affected by retained workload removal.
 */
export type EnvironmentWorkloadRetirementImpact = {
  resource: string;
  app_id: string;
  routes?: Array<DeclaredRoute>;
  job_id?: string;
  queue_binding_ids?: Array<string>;
  consumer_ids?: Array<string>;
  pending_invocations: number;
  in_flight_invocations: number;
  queued_tasks: number;
  in_flight_tasks: number;
};

