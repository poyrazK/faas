/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GitHubCheckActivity } from './GitHubCheckActivity.js';
import type { GitHubWebhookActivity } from './GitHubWebhookActivity.js';
/**
 * Bounded, redacted activity for the currently bound app. Webhook
 * payloads, GitHub delivery identifiers, retry controls, and worker
 * error text are never returned.
 *
 */
export type GitHubInstallActivity = {
  webhook_deliveries: Array<GitHubWebhookActivity>;
  check_updates: Array<GitHubCheckActivity>;
};

