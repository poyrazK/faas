/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppSecurityFinding } from './AppSecurityFinding.js';
import type { AppSecurityQuarantine } from './AppSecurityQuarantine.js';
/**
 * Read-only configuration posture and live-image scan-evidence coverage for an app, including an active image-scan quarantine when present.
 */
export type AppSecurityPostureResponse = {
  app_id: string;
  slug: string;
  profile: 'public' | 'authenticated' | 'internal';
  score: number;
  /**
   * The app's deploy-time response to high-severity configuration findings.
   */
  security_policy: 'off' | 'warn' | 'enforce';
  findings: Array<AppSecurityFinding>;
  quarantine?: (AppSecurityQuarantine | null);
};

