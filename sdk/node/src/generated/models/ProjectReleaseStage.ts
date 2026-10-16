/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseStageMember } from './ProjectReleaseStageMember.js';
export type ProjectReleaseStage = {
  id: string;
  project: string;
  environment: string;
  status: 'waiting' | 'ready' | 'blocked' | 'expired' | 'cancelled';
  reason?: string;
  created_at: string;
  expires_at: string;
  members: Array<ProjectReleaseStageMember>;
};

