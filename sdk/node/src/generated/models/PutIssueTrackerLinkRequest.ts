/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit provider ticket URL and optional title to attach to an issue group.
 */
export type PutIssueTrackerLinkRequest = {
  /**
   * Public GitHub issue or Linear issue HTTPS URL; maximum 512 UTF-8 bytes.
   */
  url: string;
  /**
   * Optional display title; maximum 256 UTF-8 bytes without control characters.
   */
  title?: string;
};

