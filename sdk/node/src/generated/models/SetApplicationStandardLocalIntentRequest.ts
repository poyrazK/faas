/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardSettings } from './ApplicationStandardSettings.js';
/**
 * Replacement local settings and additional log destinations bound to the current enrollment revision.
 */
export type SetApplicationStandardLocalIntentRequest = {
  expected_revision: number;
  settings: ApplicationStandardSettings;
  additional_log_destinations: Array<string>;
};

