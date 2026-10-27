/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One app's named log search filters.
 */
export type LogSavedSearch = {
  /**
   * Unique within the app.
   */
  name: string;
  /**
   * Canonical URL query string of filters, e.g. level=error&q=timeout.
   */
  query: string;
  created_at: string;
  updated_at: string;
};

