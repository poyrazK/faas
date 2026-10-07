/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardSettings } from './ApplicationStandardSettings.js';
import type { ApplicationStandardSource } from './ApplicationStandardSource.js';
import type { ApplicationStandardViolation } from './ApplicationStandardViolation.js';
/**
 * Resolved control values with their inheritance sources and any constraint violations.
 */
export type ApplicationStandardEffective = {
  values: ApplicationStandardSettings;
  sources: Record<string, Array<ApplicationStandardSource>>;
  violations: Array<ApplicationStandardViolation>;
};

