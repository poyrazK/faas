/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SecretRevocationTarget } from './SecretRevocationTarget.js';
/**
 * Value-free deletion status plus the exact active authorized runtime roster captured when deletion committed.
 */
export type AppSecretRevocationResponse = {
  id: string;
  scope: string;
  key: string;
  created_at: string;
  status: 'complete' | 'pending' | 'failed' | 'blocked';
  target_count: number;
  acknowledged_count: number;
  pending_count: number;
  targets: Array<SecretRevocationTarget>;
};

