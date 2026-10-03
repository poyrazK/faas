/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCustomerHealthRoute } from './RouteCustomerHealthRoute.js';
/**
 * Advisory live comparisons in the same repeatable-read snapshot as aggregate health. Reuses sample minima and consecutive-window error and selected latency checks per identity, with watched client responses included in the advisory customer summary. A confirmed observed regression takes precedence; empty, sparse, capped, unattributed, unresolved or unavailable evidence prevents a healthy summary. Healthy means the retained observed cohort comparisons passed, not proof of complete capture or a statistical SLO. Request-time tenant IDs never follow current consumer links. Revoked consumers remain eligible historical observations. No identities enter decisions, history, audits or webhooks.
 */
export type RouteCustomerHealthReport = {
  group_by: 'tenant' | 'consumer';
  details_included: boolean;
  coverage: 'observed_only';
  status: 'healthy' | 'regressed' | 'unknown' | 'disabled';
  reason: string;
  customers_limit: number;
  routes: Array<RouteCustomerHealthRoute>;
};

