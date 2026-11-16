/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A flag experiment.
 */
export type FlagExperiment = {
  key: string;
  flag: string;
  rule_id: string;
  control: string;
  metric_event: string;
  metric_kind: 'conversion';
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
};

