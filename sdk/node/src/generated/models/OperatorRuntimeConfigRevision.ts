/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One append-only runtime configuration revision.
 */
export type OperatorRuntimeConfigRevision = {
  id: number;
  key: string;
  scope: 'global' | 'control_plane' | 'daemon' | 'node';
  scope_id: string;
  version: number;
  rollout_percent: number;
  old_value: any;
  new_value: any;
  actor_id?: string;
  reason: string;
  created_at: string;
};

