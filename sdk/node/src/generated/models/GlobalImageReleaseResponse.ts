/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GlobalImageReleaseTargetResponse } from './GlobalImageReleaseTargetResponse.js';
/**
 * Durable image intent and regional acceptance outcomes. No runtime readiness or ingress claim.
 */
export type GlobalImageReleaseResponse = {
  global_app_id: string;
  release_id: string;
  generation: number;
  desired_generation: number;
  image: string;
  status: 'pending' | 'accepted' | 'expired' | 'blocked' | 'superseded';
  created_at: string;
  targets: Array<GlobalImageReleaseTargetResponse>;
};

