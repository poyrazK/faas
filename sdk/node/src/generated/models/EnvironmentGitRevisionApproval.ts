/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitReviewedMergeEvidence } from './EnvironmentGitReviewedMergeEvidence.js';
/**
 * Immutable reviewed-merge evidence bound to the approved definition and source generation.
 */
export type EnvironmentGitRevisionApproval = {
  id: string;
  source_id: string;
  revision_id: string;
  generation: number;
  definition_digest: string;
  evidence: EnvironmentGitReviewedMergeEvidence;
  recorded_at: string;
};

