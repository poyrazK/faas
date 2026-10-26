/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stamps CORS headers + handles preflight in-process.
 */
export type EdgeRuleCORSAction = {
  /**
   * Optional CORS preset reference (issue #975 #4 PR-B /
   * ADR-129). When set, the rule's resolved CORS action is
   * the merged union of the preset's fields and the rule's
   * inline fields — with the rule taking precedence for any
   * non-empty inline field. Mutually exclusive with inline
   * fields: if cors_preset_id is set, allow_origins,
   * allow_methods, allow_headers, expose_headers,
   * allow_credentials, and max_age_seconds must all be empty
   * / unset. The preset is referenced by id (UUID); an
   * invalid id (cross-tenant, deleted) causes the rule to
   * be silently dropped from the gateway's compiled slice
   * (the request matches no rule, returning 404 from the
   * route layer).
   *
   */
  cors_preset_id?: string | null;
  allow_origins: Array<string>;
  allow_methods: Array<string>;
  allow_headers?: Array<string>;
  expose_headers?: Array<string>;
  allow_credentials?: boolean;
  max_age_seconds?: number;
};

