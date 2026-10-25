/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DashboardWidget } from './DashboardWidget.js';
/**
 * Body of a dashboard create or replace.
 */
export type DashboardRequest = {
  name: string;
  widgets: Array<DashboardWidget>;
};

