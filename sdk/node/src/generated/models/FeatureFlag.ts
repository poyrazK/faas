/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagRule } from './FlagRule.js';
/**
 * Boolean application behavior flag; business logic must check it explicitly.
 */
export type FeatureFlag = {
  key: string;
  description?: string;
  enabled: boolean;
  /**
   * Value for disabled flags and unmatched customer rules.
   */
  default: boolean;
  /**
   * Server-supplied stable allocation seed; omit for a new flag.
   */
  seed?: string;
  rules: Array<FlagRule>;
};

