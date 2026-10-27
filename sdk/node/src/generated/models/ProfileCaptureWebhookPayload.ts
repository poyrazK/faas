/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCapture } from './ProfileCapture.js';
/**
 * Payload of the profile.capture_ready and profile.capture_oom_linked app webhook events. Carries the capture record and its paths, never profile samples.
 */
export type ProfileCaptureWebhookPayload = {
  version: number;
  capture: ProfileCapture;
  /**
   * Dashboard page of the capture.
   */
  capture_path: string;
  /**
   * API resource of the capture.
   */
  api_path: string;
};

