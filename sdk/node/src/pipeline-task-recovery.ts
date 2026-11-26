/** Protocol and authorization inputs for linked pipeline Task recovery. */
import { createHmac } from "node:crypto";
import { fileEventHash, fileEventUUID } from "./file-events.js";
import { jobSnapshot, jobValueHash } from "./job-runtime.js";
import { isPipelinePreviewToken, type PipelineStartPreview } from "./pipeline-start-preview.js";
import { QueryToolError, type QueryOptions } from "./query-tools.js";

export interface PipelineTaskRecoverySource {
  readonly task_id: string;
  readonly tool: string;
  readonly request_id: string;
  readonly run_id: string;
  readonly receipt_identity: string;
  readonly failure: "source_unavailable";
}

export interface PipelineTaskRecoveryPreview<P> extends PipelineStartPreview<P> {
  readonly replaces_task_id: string;
  readonly previous_source_sha256: string;
}

export interface PipelineTaskRecoveryPolicy<A> {
  readonly scopes: readonly string[];
  /** Return the reviewed pipeline's typed input after checking current access and pre-effect failure evidence. */
  prepare(source: PipelineTaskRecoverySource, principal: A, options: QueryOptions): Promise<{ readonly input: unknown; readonly previous_source_sha256: string } | null>;
}

export interface PipelineTaskRecoveryRequest {
  readonly task_id: string;
  readonly recovery_request_id: string;
  readonly expected_contract_sha256: string;
  readonly preview_token?: string;
}

export function pipelineRecoveryRequest(value: unknown, confirm: boolean): PipelineTaskRecoveryRequest {
  const args = jobSnapshot(value) as PipelineTaskRecoveryRequest;
  const keyList = confirm
    ? "expected_contract_sha256,preview_token,recovery_request_id,task_id"
    : "expected_contract_sha256,recovery_request_id,task_id";
  if (!args || typeof args !== "object" || Object.keys(args).sort().join() !== keyList ||
      !fileEventUUID(args.task_id) || !fileEventUUID(args.recovery_request_id) ||
      !fileEventHash(args.expected_contract_sha256) || (confirm && !isPipelinePreviewToken(args.preview_token)))
    throw new QueryToolError("invalid_input");
  return args;
}

/** The native request ID is server-derived. The preview exposes it only after the linked Task exists. */
export function pipelineRecoveryRequestId(key: string, owner: string, original: string, requestId: string): string {
  const bytes = createHmac("sha256", key)
    .update(jobValueHash(["gregale.pipeline-task-recovery.v1", owner, original, requestId]))
    .digest().subarray(0, 16);
  bytes[6] = (bytes[6]! & 0x0f) | 0x40;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  const hex = bytes.toString("hex");
  return hex.slice(0, 8) + "-" + hex.slice(8, 12) + "-" + hex.slice(12, 16) + "-" +
    hex.slice(16, 20) + "-" + hex.slice(20);
}
