/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolReleaseApp } from './TaskToolReleaseApp.js';
/**
 * Immutable review metadata bounded to 64 KiB. Required setup remains unverified. No source paths, executable arguments, database URLs, credentials or credential references.
 */
export type TaskToolReleaseIntent = {
  release_fingerprint: string;
  worker_runtime: 'node';
  protocol: '2026-07-28';
  observer_metric_app: string;
  endpoint: string;
  access_mode: 'open' | 'external-oauth';
  tool_scopes: Record<string, Array<string>>;
  apps: Array<TaskToolReleaseApp>;
  required_setup: Array<string>;
  reason: string;
};

