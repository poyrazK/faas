/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagLifecycle } from './FlagLifecycle.js';
/**
 * Lifecycle of every flag in the current configuration.
 */
export type FlagLifecycleReport = {
  config_version: number;
  generated_at: string;
  flags: Array<FlagLifecycle>;
};

