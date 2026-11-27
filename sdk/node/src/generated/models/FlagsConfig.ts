/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FeatureFlag } from './FeatureFlag.js';
import type { FlagHoldout } from './FlagHoldout.js';
import type { FlagLayer } from './FlagLayer.js';
/**
 * Complete atomic environment configuration; omitted flags are removed from current evaluation.
 */
export type FlagsConfig = {
  flags: Array<FeatureFlag>;
  groups: Record<string, Array<string>>;
  holdout?: FlagHoldout;
  layers?: Array<FlagLayer>;
};

