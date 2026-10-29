// src/client-preview.ts — the preview FaaSClient (ADR-20261023115741).
//
// The default entry's FaaSClient covers generally available and beta
// capabilities. This subclass, exported as FaaSClient from
// '@gregale/sdk-node/preview', adds the disposable Runs helpers, a preview
// capability that apid serves only to accounts it is released to.

import { RunsService } from './generated/services/RunsService.js';
import {
  runExecution as runExecutionRequest,
  watchExecution as watchExecutionEvents,
  type RunExecutionOptions,
  type ExecutionEvent,
  type WatchExecutionOptions,
} from './executions.js';
import type { CreateExecutionRequest } from './generated/models/CreateExecutionRequest.js';
import type { CreateExecutionArtifactGrantRequest } from './generated/models/CreateExecutionArtifactGrantRequest.js';
import type { ExecutionResponse } from './generated/models/ExecutionResponse.js';
import type { ExecutionCapabilitiesResponse } from './generated/models/ExecutionCapabilitiesResponse.js';
import type { ExecutionListResponse } from './generated/models/ExecutionListResponse.js';
import type { ExecutionWorkflowResponse } from './generated/models/ExecutionWorkflowResponse.js';
import type { ExecutionArtifactGrantResponse } from './generated/models/ExecutionArtifactGrantResponse.js';
import type { RevokeExecutionArtifactGrantResponse } from './generated/models/RevokeExecutionArtifactGrantResponse.js';
import { FaaSClient as BaseFaaSClient } from './client.js';

export class FaaSClient extends BaseFaaSClient {
  /** Read this account's Runs admission contract and plan limits. The result
   *  does not indicate scheduler or runtime image readiness. */
  getExecutionCapabilities(): Promise<ExecutionCapabilitiesResponse> {
    return RunsService.getExecutionCapabilities();
  }

  /** Read status counts and terminal-run usage for one visible workflow. */
  getExecutionWorkflow(workflowID: string): Promise<ExecutionWorkflowResponse> {
    return RunsService.getExecutionWorkflow({ workflowId: workflowID });
  }

  /** List receipts in one visible workflow, optionally narrowed by status. */
  listExecutionsForWorkflow(
    workflowID: string,
    options: { limit?: number; offset?: number; status?: ExecutionResponse['status'] } = {},
  ): Promise<ExecutionListResponse> {
    return RunsService.listExecutions({
      ...options,
      workflowId: workflowID,
    });
  }

  /** Create a short-lived, single-use capability for one output artifact.
   *  The returned token is shown once; share it only with the intended agent. */
  createExecutionArtifactGrant(
    executionID: string,
    requestBody: CreateExecutionArtifactGrantRequest,
  ): Promise<ExecutionArtifactGrantResponse> {
    return RunsService.createExecutionArtifactGrant({ id: executionID, requestBody });
  }

  /** Revoke an artifact grant before it is redeemed. */
  revokeExecutionArtifactGrant(
    grantID: string,
  ): Promise<RevokeExecutionArtifactGrantResponse> {
    return RunsService.revokeExecutionArtifactGrant({ id: grantID });
  }

  /** Stream one disposable execution until its terminal event. The
   *  iterator resumes automatically after transient disconnects. */
  watchExecution(
    executionID: string,
    options: WatchExecutionOptions = {},
  ): AsyncGenerator<ExecutionEvent, void, void> {
    return watchExecutionEvents(executionID, options);
  }

  /** Submit one disposable execution, stream/resume its events, and return
   * the terminal receipt after VM teardown. */
  runExecution(
    requestBody: CreateExecutionRequest,
    options: RunExecutionOptions = {},
  ): Promise<ExecutionResponse> {
    return runExecutionRequest(requestBody, options);
  }
}
