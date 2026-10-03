/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevBridgeCredentials } from './DevBridgeCredentials.js';
import type { DevBridgeDependency } from './DevBridgeDependency.js';
import type { DevBridgeSession } from './DevBridgeSession.js';
/**
 * New development lease with credentials returned exactly once.
 */
export type CreateDevBridgeResponse = {
  session: DevBridgeSession;
  credentials: DevBridgeCredentials;
  environment_url: string;
  dependencies?: Array<DevBridgeDependency>;
};

