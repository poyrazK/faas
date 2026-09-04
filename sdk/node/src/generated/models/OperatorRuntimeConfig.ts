/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One entry from the closed operator runtime-configuration catalog.
 */
export type OperatorRuntimeConfig = {
  key: string;
  label: string;
  description: string;
  category: string;
  kind: 'boolean' | 'integer' | 'duration' | 'string' | 'enum' | 'secret_reference';
  default_value: any;
  desired_value: any;
  effective_value: any;
  source: 'default_or_environment' | 'operator';
  apply_mode: 'hot' | 'graceful' | 'rolling' | 'break_glass';
  controller_enabled: boolean;
  mutable: boolean;
  sensitive: boolean;
  status: 'pending' | 'applied' | 'failed' | 'blocked';
  last_error?: string;
  version: number;
  updated_at?: string;
  applied_at?: string;
};

