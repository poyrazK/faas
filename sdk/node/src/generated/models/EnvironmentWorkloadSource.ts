/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Build source within the approved Git tree, a function runner, or an immutable OCI digest. Function sources require runtime and exclude dockerfile. An immutable image may explicitly opt into full-rootfs dispatch. The gated internal executor reserves new private workloads when app is omitted. Preparation does not grant serving authority.
 */
export type EnvironmentWorkloadSource = {
  /**
   * Opt into isolated canonical Git context and same-app signed build reuse. A fresh target candidate and qualification are always required. An explicit build_artifact_id takes precedence. Link entries in the context are unsupported.
   */
  reuse_unchanged_build?: boolean;
  /**
   * Repository-relative context directory; defaults to the entire repository when reuse is enabled. Must contain source.directory and all shared build inputs. Files outside this directory are not delivered to the builder. Requires reuse_unchanged_build.
   */
  build_context?: string;
  kind: 'source' | 'dockerfile' | 'image' | 'function';
  /**
   * Existing signed source-build producer. Requires build_artifact_hash; same mapped app only. Unsupported for images and scheduled Jobs.
   */
  build_artifact_id?: string;
  /**
   * Exact immutable producer input hash. Retention does not grant qualification or activation authority.
   */
  build_artifact_hash?: string;
  /**
   * Supported runner; required only when kind is function.
   */
  runtime?: 'node22' | 'python312' | 'go124' | 'go124-alpine' | 'node24' | 'python313';
  directory?: string;
  dockerfile?: string;
  image?: string;
  /**
   * Explicit full-rootfs dispatch for an immutable image source; unsupported for other source kinds.
   */
  full_rootfs?: boolean;
};

