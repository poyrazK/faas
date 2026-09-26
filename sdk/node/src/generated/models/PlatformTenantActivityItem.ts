/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugTelemetryRequestItem } from './DebugTelemetryRequestItem.js';
/**
 * One observed telemetry row and the app it belongs to. Request details contain no HTTP body or headers.
 */
export type PlatformTenantActivityItem = {
  app_id: string;
  request: DebugTelemetryRequestItem;
};

