/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectVersion } from './ObjectVersion.js';
/**
 * An owned public version page with paired continuation markers.
 */
export type ObjectVersionList = {
  items: Array<ObjectVersion>;
  common_prefixes: Array<string>;
  next_key_marker?: string;
  /**
   * Owned public version selector to resume with next_key_marker.
   */
  next_version_id_marker?: string;
};

