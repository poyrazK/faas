/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationSimulationAttempt } from './AutomationSimulationAttempt.js';
/**
 * One hypothetical root or loop item with resolved data and its control-flow decision.
 */
export type AutomationSimulationStep = {
  step_name: string;
  kind: 'run' | 'path' | 'outbound' | 'for_each' | 'join' | 'event_wait' | 'callback_wait' | 'duration_wait' | 'condition_wait';
  state: 'would_execute' | 'mocked' | 'would_wait' | 'resolved' | 'expanded' | 'skipped' | 'blocked' | 'would_retry' | 'timed_out' | 'failed' | 'dead' | 'error';
  /**
   * Stable decision reason with no referenced customer values.
   */
  reason?: string;
  blocked_by?: Array<string>;
  when_matched?: boolean;
  /**
   * Resolved action input or materialized loop source; any JSON type is preserved.
   */
  input?: any;
  /**
   * Supplied successful mock, timeout sentinel or control output; omitted when no result is known.
   */
  output?: any;
  /**
   * Safe summary of supplied action attempt outcomes; mocked error text is exposed only through eligible failure context.
   */
  attempts?: Array<AutomationSimulationAttempt>;
  run?: string;
  path?: string;
  method?: string;
  integration_id?: string;
  wait_for?: string;
  parent_step?: string;
  item_index?: number;
  item_count?: number;
};

