/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One build record: source artifact bytes, kind (app/function), status, and lifecycle timestamps.
 */
export type BuildExportResponse = {
  id: string;
  deployment_id: string;
  app_id: string;
  kind: string;
  status: string;
  source_bytes: number;
  started_at?: string | null;
  finished_at?: string | null;
};

