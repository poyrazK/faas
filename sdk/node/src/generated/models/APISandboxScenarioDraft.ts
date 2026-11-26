/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type APISandboxScenarioDraft = {
  deployment_id: string;
  source_request_id: string;
  /**
   * Current scenario revision or an empty string when the release has no saved scenario.
   */
  expected_revision: string;
  method: 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  /**
   * The matching declared route or stored OpenAPI path, when available. Replace placeholders with synthetic values before saving.
   */
  route_template?: string;
};

