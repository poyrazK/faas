/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Response shape for POST /v1/admin/github-webhook-secrets. upgraded_at is the
 * post-upsert stamp — every successful call bumps it
 * (the audit trail; an operator re-running with the same
 * secret is itself a rotation event worth recording).
 *
 */
export type AdminSetGithubWebhookSecretResponse = {
  installation_id: number;
  upgraded_at: string;
  /**
   * admin:<account_id> or platform
   */
  upgraded_by: string;
};

