/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GitHubAccountConnection } from './GitHubAccountConnection.js';
/**
 * This account's deployment connections and the CSRF token for subsequent management actions.
 */
export type GitHubAccountConnections = {
  csrf_token: string;
  connections: Array<GitHubAccountConnection>;
};

