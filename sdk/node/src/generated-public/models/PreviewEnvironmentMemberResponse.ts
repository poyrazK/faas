/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PreviewProductionChangesResponse } from './PreviewProductionChangesResponse.js';
import type { PreviewResourceLinksResponse } from './PreviewResourceLinksResponse.js';
/**
 * One expected preview workload and its latest deployment for the recorded commit.
 */
export type PreviewEnvironmentMemberResponse = {
  app_id: string;
  /**
   * Empty if the recorded app is missing.
   */
  slug: string;
  workload_name: string;
  /**
   * Includes missing when the recorded app is unavailable.
   */
  app_status: string;
  preview_state: string;
  /**
   * Empty until a deployment for the recorded commit exists.
   */
  deployment_id: string;
  /**
   * Missing until a deployment for the recorded commit exists.
   */
  deployment_status: string;
  /**
   * Preview workload expiration.
   */
  expires_at?: string;
  /**
   * Safe artifact and configuration-group comparison with the production parent; never includes secret values.
   */
  changes_from_production?: PreviewProductionChangesResponse;
  /**
   * URL and diagnostic links for this preview workload.
   */
  links?: PreviewResourceLinksResponse;
};

