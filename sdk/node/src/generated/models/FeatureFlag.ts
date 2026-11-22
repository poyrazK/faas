/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagEdge } from './FlagEdge.js';
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
   * Who decides about this flag, for example a team or an email. Not used in evaluation.
   */
  owner?: string;
  /**
   * Date after which the lifecycle report marks the flag expired. Not used in evaluation.
   */
  expires_at?: string;
  /**
   * Forces every evaluation to the application's fallback (reason killed), whatever the default, rules or inherited decisions say. Gregale's gateway also reports killed flags on each request, so a kill applies on the next request. SDKs without capability kill-v1 receive bundles without killed flags.
   */
  killed?: boolean;
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
  edge?: FlagEdge;
};

