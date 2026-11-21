/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperimentBanditArm } from './FlagExperimentBanditArm.js';
/**
 * A bandit's posterior per arm and when it last re-weighted the rule.
 */
export type FlagExperimentBandit = {
  arms: Array<FlagExperimentBanditArm>;
  weighted_at?: string;
};

