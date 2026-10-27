/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseProbe } from './ProjectReleaseProbe.js';
/**
 * Attributed candidate and retained baseline samples for a critical probe round.
 */
export type ProjectReleaseProbeEvidence = {
  probe: ProjectReleaseProbe;
  candidate_requests: number;
  candidate_errors: number;
  stable_requests: number;
  stable_errors: number;
  unauthenticated: number;
  unexpected_responses: number;
};

