/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ActivityActorResponse } from './ActivityActorResponse.js';
import type { ActivityResourceResponse } from './ActivityResourceResponse.js';
/**
 * One safe, display-ready organization activity fact.
 */
export type OrgActivityResponse = {
  /**
   * Monotonic bigint row id encoded as a string.
   */
  id: string;
  occurred_at: string;
  kind: string;
  summary: string;
  actor: ActivityActorResponse;
  resource: ActivityResourceResponse;
  app_id?: string;
  project_id?: string;
  deployment_id?: string;
  /**
   * Kind-specific non-secret display metadata.
   */
  data: Record<string, any>;
};

