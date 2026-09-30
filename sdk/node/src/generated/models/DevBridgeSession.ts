/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevBridgeScope } from './DevBridgeScope.js';
/**
 * Durable session metadata with credential digests excluded.
 */
export type DevBridgeSession = {
  id: string;
  scope: DevBridgeScope;
  expires_at: string;
  revoked_at?: string | null;
};

