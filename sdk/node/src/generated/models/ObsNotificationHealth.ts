/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsNotificationAPIStatus } from './ObsNotificationAPIStatus.js';
import type { ObsNotificationIntegration } from './ObsNotificationIntegration.js';
import type { ObsNotificationSignal } from './ObsNotificationSignal.js';
/**
 * Current pipeline evidence without per-episode delivery or human receipt claims.
 */
export type ObsNotificationHealth = {
  generated_at: string;
  state: 'healthy' | 'partial' | 'degraded';
  window_seconds: number;
  alertmanager_api: ObsNotificationAPIStatus;
  /**
   * Fixed signals for target availability, config reload, queue length and capacity, dropped alerts, notification attempts and failures.
   */
  signals: Record<string, ObsNotificationSignal>;
  /**
   * Observed integration types ordered by name; missing counters remain unknown.
   */
  integrations: Array<ObsNotificationIntegration>;
  limitations: Array<string>;
};

