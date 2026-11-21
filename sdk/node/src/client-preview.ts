// src/client-preview.ts — the preview FaaSClient (ADR-20261023115741).
//
// The default entry's FaaSClient covers generally available and beta
// capabilities. This subclass, exported as FaaSClient from
// '@gregale/sdk-node/preview', adds the disposable Runs helpers, a preview
// capability that apid serves only to accounts it is released to.

import type { CreateManagedExecutionWorkflowRequest } from './generated/models/CreateManagedExecutionWorkflowRequest.js';
import type { PublishExecutionTaskRequest } from './generated/models/PublishExecutionTaskRequest.js';
import type { DeliverRunArtifactRequest } from './generated/models/DeliverRunArtifactRequest.js';
import type { InvokeExecutionTaskRequest } from './generated/models/InvokeExecutionTaskRequest.js';
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

import { watchExecutionEnvironment, type WatchEnvironmentOptions, type EnvironmentDiagnostic } from './execution-environments.js';

import type { RunBudgetKind } from './generated/models/RunBudgetKind.js';
import type { RunBindingKind } from './generated/models/RunBindingKind.js';
import type { SetRunBudgetRequest } from './generated/models/SetRunBudgetRequest.js';
import type { SetRunAttributionBindingRequest } from './generated/models/SetRunAttributionBindingRequest.js';

import type { RerunExecutionPipelineRequest } from './generated/models/RerunExecutionPipelineRequest.js';
import { watchExecutionPipeline, type WatchPipelineOptions } from './execution-pipeline-progress.js';

import { Task } from './task.js';

/** @deprecated Use createClient from '@gregale/sdk-node/preview' and its service namespaces. */
export class FaaSClient extends BaseFaaSClient {
  task<Input, Output>(name: string, version: number, decodeResult: (value: unknown) => Output): Task<Input, Output> { return new Task(name, version, decodeResult); }
  deliverRunArtifact(slug: string, route: string, requestBody: DeliverRunArtifactRequest) { return RunsService.deliverRunArtifact({ slug, route, requestBody }); }
  getRunArtifactDelivery(slug: string, route: string, delivery: string) { return RunsService.getRunArtifactDelivery({ slug, route, delivery }); }
  signRunArtifactDelivery(slug: string, route: string, delivery: string) { return RunsService.signRunArtifactDelivery({ slug, route, delivery }); }
  getExecutionPipelineEvents(id: string, after = 0) { return RunsService.getExecutionPipelineEvents({ id, after }); }
  watchExecutionPipeline(id: string, options: WatchPipelineOptions = {}) { return watchExecutionPipeline(id, options); }
  rerunExecutionPipeline(id: string, requestBody: RerunExecutionPipelineRequest) { return RunsService.rerunExecutionPipeline({ id, requestBody }); }

  createExecutionPipeline(requestBody: CreateManagedExecutionWorkflowRequest, idempotencyKey?: string) { return RunsService.createManagedExecutionWorkflowV2({ requestBody, idempotencyKey }); }
  cancelExecutionPipeline(workflowId: string) { return RunsService.cancelExecutionPipeline({ workflowId }); }
  getRunBudget(kind: RunBudgetKind, ref: string) { return RunsService.getRunBudget({ kind, ref }); }
  setRunBudget(kind: RunBudgetKind, ref: string, requestBody: SetRunBudgetRequest) { return RunsService.setRunBudget({ kind, ref, requestBody }); }
  getRunAttributionBinding(kind: RunBindingKind, id: string) { return RunsService.getRunAttributionBinding({ kind, id }); }
  setRunAttributionBinding(kind: RunBindingKind, id: string, requestBody: SetRunAttributionBindingRequest) { return RunsService.setRunAttributionBinding({ kind, id, requestBody }); }
  deleteRunAttributionBinding(kind: RunBindingKind, id: string, expectedRevision: number) { return RunsService.deleteRunAttributionBinding({ kind, id, expectedRevision }); }
  listRunHistory(options: Parameters<typeof RunsService.listRunHistory>[0] = {}) { return RunsService.listRunHistory(options); }

  publishExecutionTask(requestBody: PublishExecutionTaskRequest) { return RunsService.publishExecutionTask({ requestBody }); }
  invokeExecutionTask(requestBody: InvokeExecutionTaskRequest, idempotencyKey: string) { return RunsService.invokeExecutionTask({ requestBody, idempotencyKey }); }
  getExecutionTask(name: string, version: number) { return RunsService.getExecutionTask({ name, version }); }
  listExecutionTasks(cursor?: string) { return RunsService.listExecutionTasks({ cursor }); }
  retireExecutionTask(name: string, version: number) { return RunsService.retireExecutionTask({ name, version }); }

  watchExecutionEnvironment(id: string, options: WatchEnvironmentOptions = {}): AsyncGenerator<EnvironmentDiagnostic> {
    return watchExecutionEnvironment(id, options);
  }

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
