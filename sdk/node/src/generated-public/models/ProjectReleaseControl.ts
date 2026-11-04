/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Most recent durable operator decision and its restoration outcome.
 */
export type ProjectReleaseControl = {
  action: 'abort' | 'recover';
  request_id: string;
  expected_generation: number;
  requested_at: string;
  status: 'pending' | 'restored' | 'blocked';
};

