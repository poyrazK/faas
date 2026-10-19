/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Policy-only eligibility; actual Slack delivery has additional subscription, routing and sender gates.
 */
export type IssueLifecycleAlertPreview = {
  issue_id: string;
  event: 'created' | 'regressed';
  environment: string;
  policy_revision: number;
  allowed: boolean;
  reason: 'eligible' | 'environment_excluded' | 'cooldown';
  next_eligible_at?: string;
};

