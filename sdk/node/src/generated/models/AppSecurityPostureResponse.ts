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
  /**
   * The app's deploy-time response to high-severity posture findings.
   */
  security_policy: 'off' | 'warn' | 'enforce';
  findings: Array<AppSecurityFinding>;
};

