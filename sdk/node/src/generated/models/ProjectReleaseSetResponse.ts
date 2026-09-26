/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseSetMemberResponse } from './ProjectReleaseSetMemberResponse.js';
/**
 * Immutable project deployment graph. Active sets do not expire; when replaced, their TTL starts and expires_at is set.
 */
export type ProjectReleaseSetResponse = {
  id: string;
  account_id: string;
  project_id: string;
  environment: string;
  active: boolean;
  ttl_seconds: number;
  expires_at?: string;
  created_at: string;
  members: Array<ProjectReleaseSetMemberResponse>;
};

