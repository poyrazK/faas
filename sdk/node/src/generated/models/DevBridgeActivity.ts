/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevBridgeRequestRecord } from './DevBridgeRequestRecord.js';
/**
 * Temporary connection and request observations without credentials or payloads.
 */
export type DevBridgeActivity = {
  connection_state: 'unknown' | 'connected' | 'disconnected' | 'expired' | 'revoked';
  connected_at?: string | null;
  disconnected_at?: string | null;
  requests: Array<DevBridgeRequestRecord>;
};

