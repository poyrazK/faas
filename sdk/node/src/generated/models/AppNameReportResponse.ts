/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppNameReportMatch } from './AppNameReportMatch.js';
/**
 * Existing apps an app name blocklist matches. source is live (app_name_blocklist) or preview (the request's blocklist). truncated means more apps matched than were listed.
 */
export type AppNameReportResponse = {
  source: 'live' | 'preview';
  /**
   * Number of blocklist entries checked.
   */
  entries: number;
  matches: Array<AppNameReportMatch>;
  truncated: boolean;
};

