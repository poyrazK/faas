/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only edge-rule subset explaining why a preview route is covered.
 */
export type AppOpenAPIPolicyPreviewRule = {
  id: string;
  match_host: string;
  match_path: string;
  match_methods: Array<string>;
  priority: number;
  enabled: boolean;
  kind: string;
  validate_mode?: string;
  action: Record<string, any>;
};

