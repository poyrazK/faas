/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One generated bundle and its bounded source-map metadata, without embedded source contents.
 */
export type IssueSourceMapArtifact = {
  /**
   * Portable relative bundle path from the Node build manifest.
   */
  generatedFile: string;
  /**
   * Producer-supplied bundle digest; the server does not retrieve bundle bytes to independently verify it.
   */
  sha256: string;
  /**
   * Flat Source Map v3 JSON; byte and decoded limits are enforced before persistence.
   */
  payload: string;
};

