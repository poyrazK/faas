/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Reviewed publication metadata bounded to 64 KiB; never includes local paths or credentials.
 */
export type ToolPublicationIntent = {
  /**
   * Reviewed canonical MCP path; new invocations require this field and never infer a missing endpoint
   */
  endpoint_path?: string;
  app: string;
  app_id?: string;
  expected_serving_deployment_id?: string;
  /**
   * Reviewed local source-tree and mode digest
   */
  source_sha256: string;
  fixtures_sha256: string;
  access_mode: 'open' | 'external-oauth';
  tool_scopes: Record<string, Array<string>>;
  test_cases: Array<string>;
};

