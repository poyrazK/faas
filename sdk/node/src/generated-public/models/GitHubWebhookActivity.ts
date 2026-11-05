/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Redacted webhook processing activity for the bound GitHub app.
 */
export type GitHubWebhookActivity = {
  event_type: string;
  status: 'pending' | 'processing' | 'succeeded' | 'dead';
  commit_sha?: string;
  received_at: string;
  processed_at?: string | null;
  updated_at: string;
  /**
   * Why a processed event deployed nothing: actions_trigger (production deploys come from a GitHub Actions workflow), branch_not_deployed, superseded (a newer commit reached the branch first), not_enabled, tag_not_branch_head, no_binding, skip_marker, invalid_release_tag, release_tag_moved or ignored. Absent when the event did its work.
   */
  skip_reason?: string;
};

