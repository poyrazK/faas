/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectSignedRequest } from './ObjectSignedRequest.js';
/**
 * Verified managed artifact and short-lived signed download request.
 */
export type JobArtifactDownloadResponse = {
  name: string;
  size_bytes: number;
  sha256: string;
  verified_at: string;
  download: ObjectSignedRequest;
};

