/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Credential-free selectors and revision metadata for one selected-app desired rule. Cache methods reflect the intersection of selector and cache action allowlist.
 */
export type RouteAdvicePolicyRule = {
  /**
   * Identifier of this consented app's policy rule.
   */
  id: string;
  /**
   * Route-advice policy kind being inspected.
   */
  kind: 'cache' | 'async' | 'throttle';
  /**
   * Whether the rule comes from the selected hostname, app-wide selector or independent environment cache policy.
   */
  source: 'host' | 'app_wide' | 'environment_policy';
  /**
   * Observed rule path selector, evaluated with gateway matching semantics.
   */
  path: string;
  /**
   * Effective policy method selectors; empty means unrestricted for non-cache rules.
   */
  methods: Array<string>;
  /**
   * Desired rule priority. Equal-priority candidates remain uncertain.
   */
  priority: number;
  /**
   * The rule requires headers or structured request conditions that telemetry does not supply.
   */
  conditional: boolean;
  /**
   * Configured enforcement mode; log-only matches do not prove an active policy effect.
   */
  mode: 'enforce' | 'log';
  /**
   * Rule update metadata used by the scoped observation fingerprint.
   */
  updated_at: string;
  /**
   * Policy expiry boundary when one is configured.
   */
  expires_at?: string;
};

