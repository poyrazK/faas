/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformFlagRule } from './PlatformFlagRule.js';
/**
 * Stored value of a platform flag. Enabled is the kill switch; default applies to accounts no rule matches.
 */
export type PlatformFlagDefinition = {
  enabled: boolean;
  default: boolean;
  rules?: Array<PlatformFlagRule>;
};

