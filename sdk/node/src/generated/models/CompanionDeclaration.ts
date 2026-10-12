/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkloadDependency } from './WorkloadDependency.js';
/**
 * Declared companion configuration only; no commands, environment values or observed health.
 */
export type CompanionDeclaration = {
  name: string;
  /**
   * Declared immutable OCI reference, when returned.
   */
  image?: string;
  preset?: 'opentelemetry' | 'sentry' | 'datadog-dogstatsd';
  type: 'init' | 'sidecar';
  /**
   * Declared memory ceiling. Zero inherits the platform default; not observed effective usage.
   */
  ram_mb: number;
  /**
   * Declared CPU ceiling. Zero inherits the platform default.
   */
  cpu_millicores: number;
  /**
   * Declared scratch ceiling. Zero derives the runtime default.
   */
  scratch_mb: number;
  disk_io_profile?: 'low' | 'standard' | 'high';
  /**
   * Declared port. Zero is not an observed listener.
   */
  port: number;
  primary_ingress: boolean;
  /**
   * Declared essential policy. Omitted uses the default true; not runtime health.
   */
  essential?: boolean;
  depends_on: Array<WorkloadDependency>;
  /**
   * Sorted unique names only, including explicit secret-binding environment names. Values and secret references are never returned.
   */
  environment_keys: Array<string>;
};

