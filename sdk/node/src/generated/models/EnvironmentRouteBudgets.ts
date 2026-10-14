/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorConfig } from './RouteMonitorConfig.js';
/**
 * Effective advisory route budgets for one immutable app environment.
 */
export type EnvironmentRouteBudgets = {
  app_id: string;
  scope: string;
  project_id?: string;
  environment_id?: string;
  budget_source: 'app_policy' | 'environment_policy';
  revision: number;
  app_revision: number;
  config: RouteMonitorConfig;
};

