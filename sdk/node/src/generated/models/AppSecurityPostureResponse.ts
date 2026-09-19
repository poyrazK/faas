/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppSecurityFinding } from './AppSecurityFinding.js';
/**
 * Read-only deterministic configuration posture for an app.
 */
export type AppSecurityPostureResponse = {
  app_id: string;
  slug: string;
  profile: 'public' | 'authenticated' | 'internal';
  score: number;
  findings: Array<AppSecurityFinding>;
};

