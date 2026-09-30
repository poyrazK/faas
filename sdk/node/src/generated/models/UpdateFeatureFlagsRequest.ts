/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagsConfig } from './FlagsConfig.js';
/**
 * Optimistic atomic configuration publication.
 */
export type UpdateFeatureFlagsRequest = {
  expected_version: number;
  config: FlagsConfig;
};

