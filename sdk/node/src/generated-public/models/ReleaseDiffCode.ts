/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * How the releases' source and image differ.
 */
export type ReleaseDiffCode = {
  changed: boolean;
  from_commit?: string;
  to_commit?: string;
  /**
   * GitHub's compare page when both releases came from the same GitHub repository.
   */
  compare_url?: string;
  image_changed: boolean;
};

