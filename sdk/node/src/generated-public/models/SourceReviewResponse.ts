/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppReleaseReview } from './AppReleaseReview.js';
/**
 * Resolved repository identity and immutable commit bound to a release review.
 */
export type SourceReviewResponse = {
  app_id: string;
  repo: string;
  commit_sha: string;
  installation_id: number;
  repository_id: number;
  source_root: string;
  release_review: AppReleaseReview;
};

