/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardSource } from './ApplicationStandardSource.js';
/**
 * A field-level constraint violation and the inherited source that imposed it.
 */
export type ApplicationStandardViolation = {
  field: string;
  code: string;
  source: ApplicationStandardSource;
};

