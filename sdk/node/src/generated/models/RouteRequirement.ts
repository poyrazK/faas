/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteChecks } from './RouteChecks.js';
/**
 * Named request shape and required configured authentication, throttle, or execution budget.
 */
export type RouteRequirement = {
  name?: string;
  method: 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'OPTIONS';
  /**
   * Concrete decoded platform-host path without templates or globs.
   */
  path: string;
  require: RouteChecks;
};

