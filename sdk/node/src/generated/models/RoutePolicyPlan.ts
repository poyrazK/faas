/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RoutePlanUnresolved } from './RoutePlanUnresolved.js';
import type { RoutePolicyChange } from './RoutePolicyChange.js';
import type { RoutePolicyRuleUsage } from './RoutePolicyRuleUsage.js';
import type { RouteRequirementsConfig } from './RouteRequirementsConfig.js';
import type { RouteRequirementsReport } from './RouteRequirementsReport.js';
/**
 * Server proposal binding normalized requirements, current configuration, options, and proposed changes. Version 2 uses concrete requirements; version 3 also binds the captured deployment contract and full inventory impact. Saved plans additionally bind requirements_revision and requirements_sha256; apply rejects changed saved intent.
 */
export type RoutePolicyPlan = {
  authority?: 'server';
  requirements?: RouteRequirementsConfig;
  throttle_burst?: number;
  /**
   * Opt-in version 3 budget synthesis within declared group prefixes, including uncaptured and future paths.
   */
  consolidate_budgets?: boolean;
  rule_usage?: RoutePolicyRuleUsage;
  version: 2 | 3;
  deployment_id?: string;
  app: string;
  app_id?: string;
  host?: string;
  plan_name?: string;
  status: 'ready' | 'partial' | 'blocked' | 'no_changes';
  sha256?: string;
  configuration_sha256: string;
  requirements_sha256: string;
  /**
   * Present when planning from saved requirements; part of the reviewed fingerprint.
   */
  requirements_revision?: number;
  scope: string;
  before: RouteRequirementsReport;
  after: RouteRequirementsReport;
  changes: Array<RoutePolicyChange>;
  unresolved: Array<RoutePlanUnresolved>;
};

