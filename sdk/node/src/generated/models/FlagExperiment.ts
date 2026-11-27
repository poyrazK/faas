/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperimentFunnel } from './FlagExperimentFunnel.js';
import type { FlagExperimentGuardrailSettings } from './FlagExperimentGuardrailSettings.js';
import type { FlagExperimentRetention } from './FlagExperimentRetention.js';
/**
 * A flag experiment.
 */
export type FlagExperiment = {
  key: string;
  flag: string;
  rule_id: string;
  control: string;
  metric_event: string;
  metric_kind: 'conversion' | 'mean' | 'funnel' | 'retention' | 'ratio' | 'percentile';
  unit: 'customer' | 'subject';
  allocation: 'fixed' | 'bandit';
  layer?: string;
  traffic?: number;
  /**
   * Start of the layer slice held while running.
   */
  layer_from?: number;
  /**
   * End (exclusive) of the layer slice held while running.
   */
  layer_to?: number;
  denominator_event?: string;
  percentile?: number;
  funnel?: FlagExperimentFunnel;
  retention?: FlagExperimentRetention;
  /**
   * Whether results are adjusted with CUPED.
   */
  cuped: boolean;
  status: 'draft' | 'running' | 'stopped';
  mixing_lift: number;
  weights: Record<string, number>;
  created_by: string;
  created_at: string;
  started_at?: string;
  stopped_at?: string;
  /**
   * End of the request telemetry the results cover.
   */
  analyzed_until?: string;
  guardrails?: FlagExperimentGuardrailSettings;
  /**
   * Control's p95 latency, fixed once control has enough requests.
   */
  latency_threshold_ms?: number;
  stop_reason?: 'manual' | 'guardrail_breach' | 'shipped';
  /**
   * The guardrail breach that stopped the experiment.
   */
  breach?: Record<string, any>;
  /**
   * The experiment's own notification channels for its decisions and problems; absent uses the environment's.
   */
  notify_channel_ids?: Array<string>;
};

