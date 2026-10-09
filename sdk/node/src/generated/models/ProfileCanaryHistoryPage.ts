/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryProfileSignal } from './CanaryProfileSignal.js';
/**
 * Bounded newest-first canary profile assessments for one deployment. Each entry pins one stage and automatic-profile policy revision. next_cursor is opaque and may expire when its retained entry is pruned.
 */
export type ProfileCanaryHistoryPage = {
  app_id: string;
  deployment_id: string;
  entries: Array<CanaryProfileSignal>;
  next_cursor?: string;
};

