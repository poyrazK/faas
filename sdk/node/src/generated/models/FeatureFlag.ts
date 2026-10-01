/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagRule } from './FlagRule.js';
import type { FlagVariant } from './FlagVariant.js';
/**
 * Application behavior flag; business logic must check it explicitly. Omitted type means boolean for backward compatibility.
 */
export type FeatureFlag = {
  key: string;
  description?: string;
  type?: 'boolean' | 'variant';
  enabled: boolean;
  /**
   * Boolean fallback for boolean flags or named fallback variant for variant flags.
   */
  default: (boolean | string);
  /**
   * Server-supplied stable allocation seed; omit for a new flag.
   */
  seed?: string;
  rules: Array<FlagRule>;
  /**
   * Required for variant flags; weights must total 10000 basis points.
   */
  variants?: Array<FlagVariant>;
};

