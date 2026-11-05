/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Strongest available non-secret deployment artifact identity.
 */
export type PreviewArtifactResponse = {
  deployment_id?: string;
  revision?: number;
  status?: string;
  image_digest?: string;
  source_sha256?: string;
  commit_sha?: string;
  build_id?: string;
};

