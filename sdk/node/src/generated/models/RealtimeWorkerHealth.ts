/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeWorkerHeartbeat } from './RealtimeWorkerHeartbeat.js';
/**
 * Realtime Worker Health.
 */
export type RealtimeWorkerHealth = {
  workers: Array<RealtimeWorkerHeartbeat>;
  live_workers: number;
  ready_workers: number;
  timeout_seconds: 60;
};

