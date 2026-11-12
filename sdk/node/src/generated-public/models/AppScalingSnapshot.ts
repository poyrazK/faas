/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppScalingSettings } from './AppScalingSettings.js';
/**
 * Current desired scaling configuration and its compare-and-swap revision.
 */
export type AppScalingSnapshot = {
  app_id: string;
  scope: string;
  /**
   * Immutable identity for a named environment.
   */
  environment_id?: string;
  revision: number;
  settings: AppScalingSettings;
};

