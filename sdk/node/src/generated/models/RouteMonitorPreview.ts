/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteMonitorReport } from './RouteMonitorReport.js';
/**
 * Read-only proposed-budget evaluation. config_change_resets_observation_anchor is true when saving the proposal would change monitor intent and start a fresh observation anchor; unchanged intent preserves its current anchor.
 */
export type RouteMonitorPreview = {
  current_revision: number;
  preview_only: boolean;
  config_change_resets_observation_anchor: boolean;
  report: RouteMonitorReport;
};

