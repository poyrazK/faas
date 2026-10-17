/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueSourceMapManifest } from './IssueSourceMapManifest.js';
/**
 * Deployment source-map publication request containing the deployed directory and portable manifest.
 */
export type PublishIssueSourceMapsRequest = {
  /**
   * Absolute deployed directory corresponding to the manifest scan root.
   */
  directory: string;
  manifest: IssueSourceMapManifest;
};

