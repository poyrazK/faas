/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppReleaseReview } from './AppReleaseReview.js';
export type TaskToolReleaseApp = {
  role: 'web' | 'worker' | 'observer' | 'previous_worker';
  app: string;
  review: AppReleaseReview;
};

