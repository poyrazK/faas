/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One invocation - metadata only, never the request or result body.
 */
export type ObsInvocationRow = {
  id: string;
  app_id: string;
  app_slug?: string;
  state: string;
  source: string;
  method: string;
  path: string;
  outcome?: string;
  attempts: number;
  last_error?: string;
  created_at: string;
  completed_at?: string | null;
};

