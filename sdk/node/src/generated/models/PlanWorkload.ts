/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ComposeHealthcheck } from './ComposeHealthcheck.js';
import type { PlanDetectedBy } from './PlanDetectedBy.js';
import type { PreviewServiceCallsPolicy } from './PreviewServiceCallsPolicy.js';
import type { ServiceBindingPolicy } from './ServiceBindingPolicy.js';
import type { ServiceBindingTransport } from './ServiceBindingTransport.js';
import type { ServiceCallerScopes } from './ServiceCallerScopes.js';
import type { ServiceReliabilityPolicies } from './ServiceReliabilityPolicies.js';
/**
 * One discovered unit of work. Mirrors reposcan.Workload.
 */
export type PlanWorkload = {
  name: string;
  /**
   * Requested customer identity policy from Compose x-gregale-platform-tenant-required or the request override. Omitted preserves existing app policy; new apps default to false.
   */
  platform_tenant_required?: boolean;
  /**
   * Effective build context inside the uploaded repository archive. Workspace manifests and sibling packages remain available outside this directory.
   */
  root_dir: string;
  dockerfile?: string;
  /**
   * Normalized prebuilt OCI image for a workload without build. Tags are resolved and pinned by the image worker before materialization; stateful images remain managed requirements. Image deployments return a deployment_id without a build_id.
   */
  image?: string;
  /**
   * Compose healthcheck override for prebuilt image workloads, captured per deployment.
   */
  image_healthcheck?: ComposeHealthcheck;
  command: Array<string>;
  /**
   * Compose service dependencies. The apply path validates the graph, deploys in dependency order, and injects GREGALE_SERVICE_<NAME>_URL plus GREGALE_SERVICE_<NAME>_HTTPS_URL for workload dependencies.
   */
  depends_on?: Array<string>;
  /**
   * Explicit Compose dependency conditions. service_started retains admission ordering; service_healthy gates release on the captured same-project, same-environment dependency deployment.
   */
  depends_on_conditions?: Record<string, 'service_started' | 'service_healthy'>;
  /**
   * Effective policy selected by Compose `x-gregale-service-policy`. New project workloads default to `declared`; existing workloads retain their persisted policy when the extension is omitted.
   */
  service_binding_policy?: ServiceBindingPolicy;
  /**
   * Dependency-specific policy selected by Compose `x-gregale-service-reliability`.
   */
  service_reliability?: ServiceReliabilityPolicies;
  /**
   * Canonical URL transport selected by Compose `x-gregale-service-transport`. Omitted preserves the established transport; new workloads default to `http`.
   */
  service_binding_transport?: ServiceBindingTransport;
  /**
   * Effective policy selected by the Compose `x-gregale-preview-calls` extension. Defaults to `allow`.
   */
  preview_service_calls_policy?: PreviewServiceCallsPolicy;
  /**
   * Target-side service allowlist from Compose `x-gregale-allow-callers`. Omitted permits same-account callers; an empty array denies all.
   */
  allowed_service_callers?: Array<string>;
  /**
   * Target-side method/path grants from Compose `x-gregale-allow-call-scopes`. When present, callers missing from the map are denied.
   */
  allowed_service_call_scopes?: ServiceCallerScopes;
  class?: 'http' | 'graphql' | 'grpc' | 'job' | 'worker' | 'server' | 'unknown';
  /**
   * cron expression when declared (CronJob, render, serverless)
   */
  schedule?: string;
  ports: Array<number>;
  /**
   * KEYS only — never values; spec §11 forbids logging secrets
   */
  env_keys?: Array<string>;
  /**
   * detector provenance, e.g. compose.yaml: api
   */
  source?: string;
  tier?: 'single' | 'convention' | 'workspace' | 'compose' | 'unknown';
  /**
   * ADR-124 blast-radius projection. create = workload is new to the account; update = existing app matches (root_dir, name).
   */
  action?: 'create' | 'update';
  /**
   * ADR-124: app row ID the update targets. Empty iff action == create.
   */
  existing_app_id?: string;
  detected_by?: PlanDetectedBy;
};

