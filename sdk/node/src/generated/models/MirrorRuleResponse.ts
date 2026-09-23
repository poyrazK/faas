/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A persisted mirror rule (issue #72 / ADR-125 / ADR-124 PR-A2).
 * `always_stripped_headers` is rendered so the customer can audit
 * what the gateway guarantees regardless of their
 * `redact_headers` setting.
 *
 */
export type MirrorRuleResponse = {
  id: string;
  account_id: string;
  app_id: string;
  source_deployment_id: string;
  mirror_deployment_id: string;
  percent: number;
  enabled: boolean;
  include_body: boolean;
  allow_unsafe_methods: boolean;
  redact_headers: Array<string>;
  /**
   * Headers the gateway ALWAYS strips regardless of the customer's redact_headers setting.
   */
  always_stripped_headers: Array<string>;
  created_at: string;
  updated_at: string;
};

