/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresSession } from './ManagedPostgresSession.js';
/**
 * Timestamped snapshot of sessions observed on the running primary.
 */
export type ManagedPostgresSessionSample = {
  sampled_at: string;
  has_more: boolean;
  sessions: Array<ManagedPostgresSession>;
};

