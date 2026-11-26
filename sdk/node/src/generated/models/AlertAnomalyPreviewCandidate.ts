/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertAnomalyPreviewBreach } from './AlertAnomalyPreviewBreach.js';
import type { AlertAnomalyWeekdayPreview } from './AlertAnomalyWeekdayPreview.js';
import type { AlertAnomalyZeroBaselineFallback } from './AlertAnomalyZeroBaselineFallback.js';
/**
 * Alternate threshold or baseline evaluated on the preview checkpoints.
 */
export type AlertAnomalyPreviewCandidate = {
  /**
   * Candidate baseline multiplier.
   */
  threshold: number;
  breaches: Array<AlertAnomalyPreviewBreach>;
  weekday_baseline?: AlertAnomalyWeekdayPreview;
  zero_baseline_fallback?: AlertAnomalyZeroBaselineFallback;
};

