/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevBucketRequest } from './DevBucketRequest.js';
import type { DevPostgresRequest } from './DevPostgresRequest.js';
import type { DevWatch } from './DevWatch.js';
/**
 * Application shape and opaque local identity for a CLI-managed developer environment.
 */
export type UpsertDevSessionRequest = {
  type?: 'app' | 'function';
  runtime?: 'node22' | 'python312' | 'go124' | 'go124-alpine' | 'node24' | 'python313' | 'binary';
  /**
   * Opaque identity derived locally from the CLI installation and canonical source path. Omit only for legacy sessions.
   */
  workspace_id?: string;
  postgres?: DevPostgresRequest;
  /**
   * Explicitly provision private workspace buckets and bindings. Requires storage:manage or admin in addition to developer session authorization. Omission preserves existing resources. Repeating a revoked binding returns conflict.
   */
  buckets?: Array<DevBucketRequest>;
  /**
   * How long the environment survives after this request. Omit for the 24-hour default. Values above the plan's developer lease maximum return 403 plan_limit_developer_lease.
   */
  lease_seconds?: number;
  watch?: DevWatch;
  /**
   * Builds cmd/<go_command> of the Go module at the source root (ADR-20261026120412), as a project service's Go command does. Omit to build what the source builds by default; applies from the next developer build. Refused with 400 for a function or a name that is not a cmd/ directory name.
   */
  go_command?: string;
};

