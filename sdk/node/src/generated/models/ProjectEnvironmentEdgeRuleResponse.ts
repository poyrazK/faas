/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One inline headers or CORS rule on a stable environment URL.
 */
export type ProjectEnvironmentEdgeRuleResponse = {
  kind: 'headers' | 'cors';
  match_path: string;
  match_methods?: Array<string>;
  match_headers?: Record<string, string>;
  priority: number;
  enabled: boolean;
  action: Record<string, any>;
};

