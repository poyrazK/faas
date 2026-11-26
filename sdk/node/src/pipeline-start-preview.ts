import { createHmac, timingSafeEqual } from "node:crypto";
import { fileEventHash } from "./file-events.js";
import { JobContract, jobValueHash, type JobSchema } from "./job-runtime.js";
import { parsePinnedPipelineSource, type PinnedPipelineSource } from "./pipeline-state.js";
import type { PipelineRunOptions } from "./job-pipeline-client.js";
import { PipelineToolError, type PipelineToolIntent } from "./pipeline-tool-state.js";

export interface PipelineStartPreviewPolicy<I, P, A> {
  readonly contract: JobContract<null, P>;
  readonly signingKey: string;
  /** Load a current, authorized source version and its proposed effects. */
  read(input: I, access: A, options: PipelineRunOptions): Promise<{
    readonly sourceVersion: string;
    readonly summary: P;
    readonly pinnedSource?: PinnedPipelineSource;
  }>;
}

export interface PipelineStartPreview<P> {
  readonly preview_token: string;
  readonly expires_at: number;
  readonly summary: P;
}

const domain = "gregale.pipeline-start-preview.v1";
const lifetimeSeconds = 300;
export const pipelinePreviewExpired = (expires: number) => expires <= Math.floor(Date.now() / 1000);
export const pipelinePreviewTokenPattern = "^[1-9][0-9]{0,12}\\.[a-f0-9]{64}$";
const tokenPattern = new RegExp(pipelinePreviewTokenPattern);
export function isPipelinePreviewToken(value: unknown): value is string {
  return typeof value === "string" && tokenPattern.exec(value)?.[0] === value;
}

export function pipelinePreviewContract(schema: JobSchema) {
  return {
    version: 1 as const, name: "pipeline-start-preview",
    input: { type: "null" as const }, output: schema,
  };
}

export function capturePipelinePreviewPolicy<I, P, A>(policy: PipelineStartPreviewPolicy<I, P, A>) {
  if (typeof policy.signingKey !== "string" || Buffer.byteLength(policy.signingKey) < 32 ||
      typeof policy.read !== "function" || policy.contract.spec.input.type !== "null")
    throw new PipelineToolError("invalid_input");
  return Object.freeze({
    signingKey: policy.signingKey,
    read: policy.read.bind(policy),
    contract: new JobContract<null, P>(policy.contract.spec),
  });
}

export async function checkPipelinePreview<I, P, A>(
  policy: PipelineStartPreviewPolicy<I, P, A>,
  intent: PipelineToolIntent,
  input: I,
  access: A,
  options: PipelineRunOptions,
  confirmation?: { readonly token: string | undefined },
): Promise<{ preview: PipelineStartPreview<P>; pinnedSource?: PinnedPipelineSource }> {
  const now = () => Math.floor(Date.now() / 1000);
  const token = confirmation?.token;
  if (confirmation && !token)
    throw new PipelineToolError("preview_required");
  const expires = token ? Number(token.split(".", 1)[0]) : now() + lifetimeSeconds;
  if (token && (!isPipelinePreviewToken(token) || pipelinePreviewExpired(expires)))
    throw new PipelineToolError("preview_stale");
  const prepared = await policy.read(input, access, options);
  if (!prepared || !fileEventHash(prepared.sourceVersion))
    throw new PipelineToolError("request_failed");
  const summary = policy.contract.output(prepared.summary);
  const pinnedSource = prepared.pinnedSource === undefined
    ? undefined
    : parsePinnedPipelineSource(prepared.pinnedSource, intent.scope, intent.owner_id);
  const fingerprint = jobValueHash([domain, intent.identity, intent.input_sha256,
    policy.contract.sha256, prepared.sourceVersion, summary, expires, ...(pinnedSource ? [pinnedSource] : [])]);
  const proof = expires + "." + createHmac("sha256", policy.signingKey).update(fingerprint).digest("hex");
  if (pipelinePreviewExpired(expires) || (token && !timingSafeEqual(Buffer.from(token), Buffer.from(proof))))
    throw new PipelineToolError("preview_stale");
  return { preview: { summary, preview_token: proof, expires_at: expires }, ...(pinnedSource && { pinnedSource }) };
}
