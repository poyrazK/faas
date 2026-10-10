/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained event-publication acceptance and a guarded URL for subscriber delivery evidence.
 */
export type CommitEventPublication = {
  acceptance_id: number;
  source: string;
  id: string;
  accepted_at: string;
  /**
   * Read-only event receipt with an acceptance-time guard; exposes paginated subscriber delivery evidence while retained.
   */
  receipt_url: string;
};

