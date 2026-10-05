/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardReviewedApp } from './ApplicationStandardReviewedApp.js';
/**
 * One application target and its installation and observation progress in a controlled rollout.
 */
export type ApplicationStandardOperationTarget = {
  app_id: string;
  position: number;
  approved_app: ApplicationStandardReviewedApp;
  state: 'queued' | 'applying' | 'blocked' | 'persisted' | 'observed' | 'skipped';
  desired_revision: number;
  error_code?: string;
  updated_at: string;
};

