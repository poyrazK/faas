/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Authorized deployment CPU capture window.
 */
export type ProfileQuery = {
  /**
   * Optional static METHOD /pattern or [unattributed]; empty selects all sampled CPU.
   */
  route?: string;
  deployment_id: string;
  runtime: string;
  start: string;
  end: string;
};

