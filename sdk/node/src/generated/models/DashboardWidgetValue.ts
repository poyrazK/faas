/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DashboardPoint } from './DashboardPoint.js';
import type { DashboardWidget } from './DashboardWidget.js';
/**
 * A widget with its resolved value, or the reason it has none.
 */
export type DashboardWidgetValue = (DashboardWidget & {
  /**
   * The headline number, in unit.
   */
  value?: number;
  /**
   * ms, %, requests, events, gauge, per second, or % budget left.
   */
  unit?: string;
  /**
   * Context: log counts by level, SLO attainment.
   */
  detail?: string;
  /**
   * The custom metric's points over the window.
   */
  series?: Array<DashboardPoint>;
  /**
   * Why the widget has no value: an unavailable app or source, no data, or a plan gate.
   */
  error?: string;
});

