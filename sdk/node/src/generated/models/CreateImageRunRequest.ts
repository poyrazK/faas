/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ImageRunRegistryAuth } from './ImageRunRegistryAuth.js';
/**
 * Body of POST /v1/image-runs (ADR-1161).
 */
export type CreateImageRunRequest = {
  /**
   * OCI image reference; normalized before use (alpine → docker.io/library/alpine:latest).
   */
  image: string;
  /**
   * Replaces the image ENTRYPOINT; without args the image CMD is dropped.
   */
  entrypoint?: Array<string>;
  /**
   * Replaces the image CMD.
   */
  args?: Array<string>;
  /**
   * Per-run environment, validated like Job run env_overrides.
   */
  env?: Record<string, string>;
  /**
   * Task memory in MB; 0 means the Job default. Capped by the plan's Job RAM ceiling.
   */
  ram_mb?: number;
  /**
   * Task deadline in seconds; 0 means the plan's Job task timeout ceiling.
   */
  timeout_sec?: number;
  /**
   * always re-pulls a cached image so a moved tag is resolved again.
   */
  pull?: 'missing' | 'always';
  /**
   * ADR-1330: hold the command until one client attaches to the run's task. The run has no retries and timeout_sec is at most 3600. Needs the operator gate FAAS_INTERACTIVE_IMAGE_RUNS; otherwise 501.
   */
  interactive?: boolean;
  /**
   * Give the interactive command a pseudo-terminal. Requires interactive.
   */
  tty?: boolean;
  /**
   * ADR-1790: run as name|uid[:group|gid], resolved in the image's passwd and group files, instead of the image USER (docker run -u).
   */
  user?: string;
  /**
   * ADR-1790: absolute working directory instead of the image WORKDIR (docker run -w); created when missing.
   */
  workdir?: string;
  registry_auth?: ImageRunRegistryAuth;
};

