/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One change timeline entry with a platform-generated summary.
 */
export type AppChangeEvent = {
  at: string;
  source: 'deployment' | 'edge_rule' | 'runtime_config' | 'incident' | 'health' | 'activity';
  /**
   * Source-specific kind such as deploy.rolled_back or env.set.
   */
  kind: string;
  deployment_id?: string;
  summary: string;
};

