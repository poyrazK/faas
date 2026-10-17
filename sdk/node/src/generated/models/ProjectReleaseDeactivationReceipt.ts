/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseSetMemberResponse } from './ProjectReleaseSetMemberResponse.js';
/**
 * Recorded deactivation decision and exact fallback routes reviewed for a project graph.
 */
export type ProjectReleaseDeactivationReceipt = {
  id: string;
  release_id: string;
  completed_at: string;
  fallback_members: Array<ProjectReleaseSetMemberResponse>;
};

