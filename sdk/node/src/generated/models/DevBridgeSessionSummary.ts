/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevBridgeSession } from './DevBridgeSession.js';
/**
 * A durable session and its informational observed connection state.
 */
export type DevBridgeSessionSummary = {
  session: DevBridgeSession;
  connection_state: 'unknown' | 'connected' | 'disconnected' | 'expired' | 'revoked';
};

