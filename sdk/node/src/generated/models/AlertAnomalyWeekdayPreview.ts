/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertAnomalyPreviewSkippedWindow } from './AlertAnomalyPreviewSkippedWindow.js';
/**
 * Coverage for the same-weekday candidate.
 */
export type AlertAnomalyWeekdayPreview = {
  /**
   * Checks the candidate could evaluate, including checks using the seven-day fallback.
   */
  evaluated: number;
  skipped_windows: Array<AlertAnomalyPreviewSkippedWindow>;
  /**
   * Covered check times where fewer than three usable matching weekdays caused the candidate to use the seven-day baseline. Includes any checks with insufficient seven-day history.
   */
  fallback_at: Array<string>;
};

