/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GitHubConnectionVerification } from './GitHubConnectionVerification.js';
/**
 * Credential-free health and access summary for one account-owned deployment authorization.
 */
export type GitHubAccountConnection = {
  access?: GitHubConnectionVerification;
  installation_id: number;
  github_login: string;
  health: 'unknown' | 'healthy' | 'degraded';
  repository_count: number;
  last_verified_at?: string;
};

