/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Selected immutable public version and the acknowledged marker flag after permanent deletion.
 */
export type ObjectVersionDeleteResult = {
  /**
   * Public immutable version UUID or null.
   */
  version_id: string;
  /**
   * True when the acknowledged removal deleted a marker. A retry after removal can return false.
   */
  delete_marker: boolean;
};

