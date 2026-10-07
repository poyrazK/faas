/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionArtifactInput } from './ExecutionArtifactInput.js';
import type { ExecutionFile } from './ExecutionFile.js';
import type { ExecutionLimitRequest } from './ExecutionLimitRequest.js';
import type { ExecutionNetworkPolicy } from './ExecutionNetworkPolicy.js';
/**
 * Source and JSON input for one disposable execution. Send either the
 * legacy `source` string or an ephemeral `files` bundle with an
 * `entrypoint`. `artifact_inputs` may add files from successful runs in
 * the same key family for Runs-only credentials, or from the same account
 * for broad credentials; they are copied into the new
 * encrypted request and staged only in its ephemeral guest filesystem.
 * Optional `workflow_id` and `step_label` values group run receipts in
 * control-plane metadata; the guest does not receive them.
 * Optional `integration_ids` explicitly select managed integrations
 * granted for this Run; the IDs remain control-plane metadata and are
 * not delivered to the guest payload.
 * v1 supports only the listed interpreter runtimes and
 * `network.mode=none`; dependencies, secrets, environment injection, and
 * persistent disks are not part of this contract.
 *
 */
export type CreateExecutionRequest = {
  /**
   * Optional caller-generated grouping id; control-plane metadata only.
   */
  workflow_id?: string;
  /**
   * Optional bounded label for this run within its workflow; requires workflow_id. Agent workflow labels beginning with gwf: are unique per workflow and API-key family.
   */
  step_label?: string;
  /**
   * Optional account integration IDs explicitly granted for this Run; control-plane metadata only.
   */
  integration_ids?: Array<string>;
  /**
   * Immutable preinstalled dependency profile. python-data-v1 requires python313; standard preserves standard-library-only execution.
   */
  profile?: 'standard' | 'python-data-v1';
  runtime: 'node22' | 'node24' | 'python312' | 'python313';
  /**
   * Single-file source code; never returned by execution reads.
   */
  source?: string;
  /**
   * Normalized relative path of the file to execute when `files` is supplied.
   */
  entrypoint?: string;
  /**
   * Regular files staged into the guest's ephemeral scratch filesystem.
   */
  files?: Array<ExecutionFile>;
  /**
   * Artifacts from successful executions visible to this credential, copied to paths in the ephemeral files bundle before sealing.
   */
  artifact_inputs?: Array<ExecutionArtifactInput>;
  /**
   * Explicit normalized relative paths below context.output_dir to export on success. No globs. Missing, symlink, and special files fail the run.
   */
  output_files?: Array<string>;
  /**
   * One complete JSON value delivered to the guest as input.
   */
  input?: any;
  limits?: ExecutionLimitRequest;
  network?: ExecutionNetworkPolicy;
};

