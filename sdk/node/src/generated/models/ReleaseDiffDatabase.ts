/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * How the releases' release commands, which run database migrations, differ (ADR-1448).
 */
export type ReleaseDiffDatabase = {
  changed: boolean;
  from_release_command?: string;
  to_release_command?: string;
};

