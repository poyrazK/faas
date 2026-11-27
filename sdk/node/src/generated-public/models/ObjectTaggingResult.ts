/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Acknowledged object tags and optional owned public version identity.
 */
export type ObjectTaggingResult = {
  /**
   * Public version UUID or null. Omitted for unversioned or legacy current-object providers.
   */
  version_id?: string;
  /**
   * Acknowledged complete tag set; native provider IDs and private completion metadata are never exposed.
   */
  tags: Record<string, string>;
};

