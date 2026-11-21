/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperiment } from './FlagExperiment.js';
import type { FlagExperimentArm } from './FlagExperimentArm.js';
import type { FlagExperimentBandit } from './FlagExperimentBandit.js';
import type { FlagExperimentCuped } from './FlagExperimentCuped.js';
import type { FlagExperimentGuardrail } from './FlagExperimentGuardrail.js';
import type { FlagExperimentLatency } from './FlagExperimentLatency.js';
import type { FlagExperimentRecommendation } from './FlagExperimentRecommendation.js';
import type { FlagExperimentSegment } from './FlagExperimentSegment.js';
import type { FlagExperimentVariantResult } from './FlagExperimentVariantResult.js';
/**
 * A flag experiment's sequential results.
 */
export type FlagExperimentResults = {
  experiment: FlagExperiment;
  control: FlagExperimentArm;
  variants: Array<FlagExperimentVariantResult>;
  sample_ratio_p_value: number;
  sample_ratio_mismatch: boolean;
  /**
   * Customers that saw more than one variant and are left out.
   */
  crossed_units: number;
  guardrails?: Array<FlagExperimentGuardrail>;
  latency?: Array<FlagExperimentLatency>;
  recommendation?: FlagExperimentRecommendation;
  cuped: FlagExperimentCuped;
  /**
   * Percentile results: bootstrap p-values without a sequential guarantee; read them once, at the planned end.
   */
  fixed_horizon?: boolean;
  bandit?: FlagExperimentBandit;
  /**
   * Results split by ?segment=COHORT: its members, then everyone else.
   */
  segments?: Array<FlagExperimentSegment>;
};

