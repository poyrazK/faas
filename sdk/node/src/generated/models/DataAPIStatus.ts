/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataAPIChangeHookStatus } from './DataAPIChangeHookStatus.js';
/**
 * What the platform knows about a Data API without waking it.
 */
export type DataAPIStatus = {
  /**
   * The runtime version this control plane serves; running Data APIs report theirs as runtime_version on /healthz.
   */
  runtime_version: string;
  /**
   * unavailable when this installation accepts no runtime reports: usage reports, monthly caps, keyless live reads and change hooks then do not reach the platform.
   */
  runtime_reports: 'available' | 'unavailable';
  change_hooks?: DataAPIChangeHookStatus;
};

