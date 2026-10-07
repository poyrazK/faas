/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardReviewBlocker } from './ApplicationStandardReviewBlocker.js';
import type { ApplicationStandardReviewedApp } from './ApplicationStandardReviewedApp.js';
import type { ApplicationStandardReviewRequest } from './ApplicationStandardReviewRequest.js';
/**
 * Immutable saved preview. Its hash binds private authoritative inputs. Expiry does not prevent historical inspection; approval always requires fresh validation.
 */
export type ApplicationStandardReview = {
  id: string;
  org_id: string;
  created_by: string;
  request: ApplicationStandardReviewRequest;
  approval_hash: string;
  applications: Array<ApplicationStandardReviewedApp>;
  blockers: Array<ApplicationStandardReviewBlocker>;
  created_at: string;
  expires_at: string;
};

