/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardAdoption } from './ApplicationStandardAdoption.js';
import type { ApplicationStandardEffective } from './ApplicationStandardEffective.js';
import type { ApplicationStandardSettings } from './ApplicationStandardSettings.js';
/**
 * Application inputs, adoption pins and resolved changes captured in a saved review.
 */
export type ApplicationStandardReviewedApp = {
  app_id: string;
  slug: string;
  project_id?: string;
  desired_revision: number;
  before_settings: ApplicationStandardSettings;
  before_adoptions: Array<ApplicationStandardAdoption>;
  after_adoptions: Array<ApplicationStandardAdoption>;
  local_settings: ApplicationStandardSettings;
  additional_log_destinations: Array<string>;
  effective: ApplicationStandardEffective;
  changed_fields: Array<'log_destinations' | 'require_signed' | 'security_policy' | 'trusted_publishers' | 'egress_cidrs' | 'egress_extra_ports'>;
};

