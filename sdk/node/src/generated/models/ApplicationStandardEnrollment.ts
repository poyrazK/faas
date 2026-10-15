/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardAdoption } from './ApplicationStandardAdoption.js';
import type { ApplicationStandardEffective } from './ApplicationStandardEffective.js';
import type { ApplicationStandardSettings } from './ApplicationStandardSettings.js';
/**
 * Saved desired intent and the last installed projection, separately from consumer observation.
 */
export type ApplicationStandardEnrollment = {
  app_id: string;
  org_id: string;
  project_id?: string;
  local_settings: ApplicationStandardSettings;
  additional_log_destinations: Array<string>;
  /**
   * Captured adoption pins; publication never moves them automatically.
   */
  adoptions: Array<ApplicationStandardAdoption>;
  materialized_fields: Array<'log_destinations' | 'require_signed' | 'security_policy' | 'trusted_publishers' | 'egress_cidrs' | 'egress_extra_ports'>;
  installed_effective?: ApplicationStandardEffective;
  installed_effective_hash?: string;
  /**
   * Deadline of a contributing exception in the last persisted projection; it can already be expired while replacement is pending. Does not establish consumer observation.
   */
  installed_exception_expires_at?: string;
  desired_revision: number;
  persisted_revision: number;
  observed_revision: number;
  state: 'unmanaged' | 'pending' | 'applying' | 'blocked' | 'persisted' | 'observed';
  error_code?: string;
  updated_at: string;
};

