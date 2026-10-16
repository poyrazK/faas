/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteAdvicePolicyRule } from './RouteAdvicePolicyRule.js';
/**
 * Bounded observation of selected-app desired cache, async and throttle rules matching the selected host, plus app-wide rules. Omits action and condition values; not proof of gateway convergence or authority to write.
 */
export type RouteAdvicePolicyContext = {
  /**
   * Policy snapshot availability; changed means re-reading found a different observation.
   */
  status: 'observed' | 'unavailable' | 'changed';
  /**
   * Opaque fingerprint of scoped rule identities and update metadata; present only for observed snapshots, never an apply guard or account activity counter.
   */
  revision?: string;
  /**
   * Time the desired-policy observation was collected.
   */
  observed_at: string;
  /**
   * Exact selected platform or immutable named-environment hostname.
   */
  host?: string;
  rules: Array<RouteAdvicePolicyRule>;
  /**
   * Stable coverage reasons. Runtime request attributes, disabled drafts, other gateway gates and actual convergence are not established by this read.
   */
  gaps: Array<string>;
};

