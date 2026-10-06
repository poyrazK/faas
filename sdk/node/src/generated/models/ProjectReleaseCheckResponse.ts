/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckFinding } from './BindingCheckFinding.js';
import type { BindingCheckReport } from './BindingCheckReport.js';
import type { ProjectReleaseSetMemberResponse } from './ProjectReleaseSetMemberResponse.js';
/**
 * Observation of the complete exact deployment graph and its binding evidence. Publication evaluates fresh evidence and compares the active predecessor again.
 */
export type ProjectReleaseCheckResponse = {
  project_id: string;
  environment: string;
  expected_active_release_id: string;
  graph_digest: string;
  ttl_seconds: number;
  members: Array<ProjectReleaseSetMemberResponse>;
  passed: boolean;
  checked_at: string;
  checks: Array<BindingCheckReport>;
  blockers?: Array<BindingCheckFinding>;
};

