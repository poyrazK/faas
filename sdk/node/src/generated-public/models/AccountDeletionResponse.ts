/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Result of staging account deletion: status flips to `deleted_pending` and `restore_until` marks the end of the 30-day grace window.
 */
export type AccountDeletionResponse = {
  status: 'deleted_pending';
  scheduled_at: string;
  restore_until: string;
};

