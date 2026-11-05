/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DashboardWidget } from './DashboardWidget.js';
/**
 * An account's saved dashboard.
 */
export type Dashboard = {
  id: string;
  name: string;
  widgets: Array<DashboardWidget>;
  created_at: string;
  updated_at: string;
};

