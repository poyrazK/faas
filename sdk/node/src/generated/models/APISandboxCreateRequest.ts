/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Configuration for a fresh copy of an API release.
 */
export type APISandboxCreateRequest = {
  /**
   * Optional absolute path on the sandbox host to check with GET. Use the same value when retrying with this sandbox UUID.
   */
  smoke_path?: string;
  /**
   * Copy the selected app library or release scenario revision into the sandbox. Reuse this value when retrying the same sandbox UUID.
   */
  scenario_revision?: string;
  /**
   * Optional recipient label. Requires scenario_revision and creates a private copy. Raw partner progress is available to the owner.
   */
  partner_label?: string;
};

