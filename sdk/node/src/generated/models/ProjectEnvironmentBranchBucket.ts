/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One overlay bucket a branch owns on a parent bucket. The branch reads every key it does not hold itself from the parent.
 */
export type ProjectEnvironmentBranchBucket = {
  source_bucket_id: string;
  /**
   * The parent bucket's name.
   */
  name?: string;
  /**
   * Empty until the bucket is made.
   */
  copy_bucket_id?: string;
  /**
   * as_of when the branch reads the parent as of the branch point (the parent had versioning on when the branch was made); live_parent when it reads the parent's current objects.
   */
  consistency?: 'as_of' | 'live_parent';
  /**
   * The instant an as_of bucket reads the parent at.
   */
  as_of?: string;
};

