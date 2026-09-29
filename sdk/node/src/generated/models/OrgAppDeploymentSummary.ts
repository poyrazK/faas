/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe deployment status projection for an application shared in a workspace. Actor attribution is on the organization activity timeline; detailed deployment fields stay creator-scoped.
 */
export type OrgAppDeploymentSummary = {
  id: string;
  /**
   * Per-app revision when available.
   */
  revision?: number;
  /**
   * Deployment source kind.
   */
  kind: 'image' | 'tarball' | 'dockerfile' | 'github' | 'preview';
  /**
   * Current deployment lifecycle status.
   */
  status: 'pending' | 'building' | 'imaging' | 'snapshotting' | 'live' | 'failed' | 'superseded' | 'cancelled';
  created_at: string;
};

