/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An existing app whose name a blocklist entry matches.
 */
export type AppNameReportMatch = {
  slug: string;
  app_id: string;
  account_id: string;
  org_id?: string;
  status: string;
  /**
   * The blocklist entry that matched.
   */
  entry: string;
};

