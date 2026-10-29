/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HostingRouteCheckSet } from './HostingRouteCheckSet.js';
/**
 * Candidate hosting verification recovery progress. last_error_code distinguishes publication, gateway, transport and candidate-evidence failures; a transport failure does not attribute blame to the app or platform. retry_not_before is an eligibility floor, not a promised delivery time; completed_at means the attempt finished, while the hosting receipt records its verdict.
 */
export type DeploymentHostingVerification = {
  started_at: string;
  deadline_at: string;
  attempts: number;
  last_error_code?: string;
  last_route_checks?: HostingRouteCheckSet;
  retry_not_before?: string;
  completed_at?: string;
};

