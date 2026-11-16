/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppReleaseReview } from './AppReleaseReview.js';
/**
 * One existing personal app role and its exact default-environment native release snapshot.
 */
export type TaskToolReleaseApp = {
  role: 'web' | 'worker' | 'observer' | 'previous_worker';
  app: string;
  review: AppReleaseReview;
};

