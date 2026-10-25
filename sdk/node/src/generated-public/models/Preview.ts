/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One self-serve platform preview (ADR 20261021080640).
 */
export type Preview = {
  key: string;
  label: string;
  description: string;
  /**
   * Planned end of the preview, by general availability or removal.
   */
  retire_by?: string;
  /**
   * Whether you (or, under /v1/orgs/{slug}, the organization) opted in.
   */
  joined: boolean;
  /**
   * Whether the preview is open to self-serve and your plan may join it.
   */
  eligible: boolean;
  /**
   * Whether the preview is on for you now, for any reason.
   */
  enabled: boolean;
};

