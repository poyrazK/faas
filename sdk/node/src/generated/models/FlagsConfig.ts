/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FeatureFlag } from './FeatureFlag.js';
/**
 * Complete atomic environment configuration; omitted flags are removed from current evaluation.
 */
export type FlagsConfig = {
  flags: Array<FeatureFlag>;
  groups: Record<string, Array<string>>;
};

