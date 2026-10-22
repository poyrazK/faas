/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Draft policy and hypothetical lifecycle event for an existing issue.
 */
export type PreviewIssueLifecycleAlertRequest = {
  issue_id: string;
  event: 'created' | 'regressed';
  /**
   * Draft environment allowlist used only for this preview; an empty array matches all.
   */
  environments: Array<string>;
  cooldown_seconds: number;
};

