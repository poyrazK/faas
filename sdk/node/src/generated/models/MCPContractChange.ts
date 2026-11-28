/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One difference between a candidate's MCP catalog and the promised contract.
 */
export type MCPContractChange = {
  tool?: string;
  resource?: string;
  resource_template?: string;
  prompt?: string;
  capability?: string;
  /**
   * JSON pointer into the contract.
   */
  path: string;
  kind: string;
  severity: 'informational' | 'needs_review' | 'breaking';
};

