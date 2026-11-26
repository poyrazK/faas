/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertAnomalyIncidentReplay } from './AlertAnomalyIncidentReplay.js';
import type { AlertAnomalyPreviewBreach } from './AlertAnomalyPreviewBreach.js';
import type { AlertAnomalyPreviewCandidate } from './AlertAnomalyPreviewCandidate.js';
import type { AlertAnomalyPreviewSkippedWindow } from './AlertAnomalyPreviewSkippedWindow.js';
/**
 * Historical anomaly evaluations with coverage, breaches, and optional replay results.
 */
export type AlertAnomalyPreviewResponse = {
  /**
   * Environment used for both observed traffic and historical baselines. Present for environment-specific replays.
   */
  environment_scope?: string;
  /**
   * Environments available for incident replay. Present in the hourly overview. Recent telemetry may still be unavailable.
   */
  environment_scopes?: Array<string>;
  first_check_at: string;
  last_check_at: string;
  step_seconds: 60 | 3600;
  /**
   * Windows with enough data to evaluate
   */
  evaluated: number;
  /**
   * Checks with missing telemetry, insufficient baseline history, or too few eligible requests.
   */
  skipped: number;
  skipped_windows: Array<AlertAnomalyPreviewSkippedWindow>;
  breaches: Array<AlertAnomalyPreviewBreach>;
  candidate?: AlertAnomalyPreviewCandidate;
  incident_replay?: AlertAnomalyIncidentReplay;
};

