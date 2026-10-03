/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PreflightFinding } from './PreflightFinding.js';
import type { PreflightLevel } from './PreflightLevel.js';
import type { PreflightProfile } from './PreflightProfile.js';
/**
 * The assessed result for one source tree: a headline level, the findings
 * behind it, and the run contract that was inferred.
 *
 */
export type PreflightVerdict = {
  level: PreflightLevel;
  findings?: Array<PreflightFinding>;
  profile: PreflightProfile;
};

