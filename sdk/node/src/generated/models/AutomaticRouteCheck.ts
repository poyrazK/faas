/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCheckChanges } from './RouteCheckChanges.js';
import type { RouteRequirementsCheck } from './RouteRequirementsCheck.js';
/**
 * Latest bounded deployment verdict and independently computed freshness. Recent completed checks have bounded immutable history and finding deltas; history is not current passing evidence. Queue runs after capture or saved intent changes; refresh reevaluates policy drift.
 */
export type AutomaticRouteCheck = {
  version: number;
  app: string;
  app_id: string;
  deployment_id: string;
  state: 'pending' | 'running' | 'retrying' | 'complete';
  freshness: 'unavailable' | 'current' | 'stale';
  stale_reasons: Array<'requirements_changed' | 'capture_changed' | 'configuration_changed'>;
  current_requirements_revision: number;
  current_requirements_sha256: string;
  attempts: number;
  last_error_code?: 'check_failed';
  queued_at: string;
  next_attempt_at?: string;
  checked_at?: string;
  /**
   * Identity of the stored completed check; may refer to a historical verdict while work is pending.
   */
  check_id?: string;
  changes?: RouteCheckChanges;
  check?: RouteRequirementsCheck;
};

