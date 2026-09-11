/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Confirmation that one recovery item moved from dead back to pending.
 */
export type GithubRecoveryRetryResponse = {
  ok: boolean;
  kind: 'delivery' | 'check_update';
  target_id: string;
  status: string;
};

