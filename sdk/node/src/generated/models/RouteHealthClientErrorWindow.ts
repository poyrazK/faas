/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteHealthStatusCounts } from './RouteHealthStatusCounts.js';
/**
 * Candidate/stable observations for one watched code within the shared aggregate observation window. Only deployment-attributed responses participate; pre-routing rejections without a deployment cannot be compared.
 */
export type RouteHealthClientErrorWindow = {
  start: string;
  end: string;
  candidate: RouteHealthStatusCounts;
  stable: RouteHealthStatusCounts;
  status: 'healthy' | 'regressed' | 'unknown';
  reason: string;
};

