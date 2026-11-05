/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SessionInfo } from './SessionInfo.js';
/**
 * Body for `GET /v1/auth/sessions`.
 */
export type SessionListResponse = {
  /**
   * Active sessions, newest first. Revoked rows are not returned.
   */
  sessions: Array<SessionInfo>;
};

