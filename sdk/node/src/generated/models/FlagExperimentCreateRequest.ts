/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperimentFunnel } from './FlagExperimentFunnel.js';
import type { FlagExperimentGuardrailsRequest } from './FlagExperimentGuardrailsRequest.js';
import type { FlagExperimentRetention } from './FlagExperimentRetention.js';
/**
 * A flag experiment to create.
 */
export type FlagExperimentCreateRequest = {
  key: string;
  /**
   * The variant flag.
   */
  flag: string;
  /**
   * The flag's weighted rule.
   */
  rule_id: string;
  /**
   * The baseline variant.
   */
  control: string;
  /**
   * The metric event; required for conversion and mean metrics, refused for funnel and retention.
   */
  metric_event?: string;
  /**
   * conversion (default) compares the share of exposed customers that sent the event; mean compares the mean event value per exposed customer; funnel and retention compare the share that completed the funnel or came back, and need product analytics.
   */
  metric_kind?: 'conversion' | 'mean' | 'funnel' | 'retention';
  funnel?: FlagExperimentFunnel;
  retention?: FlagExperimentRetention;
  /**
   * Adjust results for each customer's 14 days before the start (CUPED); default true.
   */
  cuped?: boolean;
  /**
   * mSPRT mixing standard deviation: an absolute conversion-rate lift (default 0.02), or a standardized effect size for mean metrics (default 0.1).
   */
  mixing_lift?: number;
  guardrails?: FlagExperimentGuardrailsRequest;
  /**
   * Start the experiment at once.
   */
  start?: boolean;
};

