/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentResponse } from '../models/DeploymentResponse.js';
import type { UploadSessionResponse } from '../models/UploadSessionResponse.js';
import type { UploadStartRequest } from '../models/UploadStartRequest.js';
import type { UploadStartResponse } from '../models/UploadStartResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class UploadsService {
  /**
   * Open a resumable upload session.
   * @returns UploadStartResponse Session opened.
   * @throws ApiError
   */
  public static startUpload({
    requestBody,
  }: {
    requestBody: UploadStartRequest,
  }): CancelablePromise<UploadStartResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/uploads',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        413: `code: source_too_large`,
        429: `429. Two response shapes:
        - \`application/problem+json\` for code-driven 429s (\`plan_limit_concurrency\`, \`quota_exhausted\`).
        - \`text/plain\` for the authlimiter middleware (\`pkg/middleware/authlimit.go\`).
        `,
      },
    });
  }
  /**
   * Discover resumable session state and current offset.
   * @returns UploadSessionResponse Current session state.
   * @throws ApiError
   */
  public static getUploadSession({
    id,
  }: {
    /**
     * Upload session id (returned by POST /v1/uploads).
     */
    id: string,
  }): CancelablePromise<UploadSessionResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/uploads/{id}',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Append a chunk to an open session.
   * @returns string Bytes accepted.
   * @throws ApiError
   */
  public static appendUpload({
    id,
    uploadOffset,
    requestBody,
  }: {
    /**
     * Upload session id (returned by POST /v1/uploads).
     */
    id: string,
    /**
     * Absolute byte offset the client claims the server is at.
     */
    uploadOffset: number,
    requestBody: Blob,
  }): CancelablePromise<string> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/uploads/{id}',
      path: {
        'id': id,
      },
      headers: {
        'Upload-Offset': uploadOffset,
      },
      body: requestBody,
      mediaType: 'application/offset+octet-stream',
      responseHeader: 'Upload-Offset',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
        413: `code: payload_too_large — the PATCH chunk body exceeds the per-plan or per-account cap. Distinct from \`source_too_large\` (POST /v1/uploads when total_size exceeds SourceTarballMaxMB), this fires mid-upload when the customer's chunk size or accumulated spool crosses the limit.`,
      },
    });
  }
  /**
   * Cancel an open session (removes the .part file).
   * @returns void
   * @throws ApiError
   */
  public static cancelUpload({
    id,
  }: {
    /**
     * Upload session id (returned by POST /v1/uploads).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/uploads/{id}',
      path: {
        'id': id,
      },
      errors: {
        404: `code: not_found`,
        409: `code: conflict`,
      },
    });
  }
  /**
   * Finalize the session, validate the tarball, enqueue the build.
   * @returns DeploymentResponse Deployment created.
   * @throws ApiError
   */
  public static commitUpload({
    id,
  }: {
    /**
     * Upload session id to finalize (returned by POST /v1/uploads).
     */
    id: string,
  }): CancelablePromise<DeploymentResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/uploads/{id}/commit',
      path: {
        'id': id,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        404: `code: not_found`,
        409: `code: conflict`,
        410: `code: upload_session_expired — the resumable-upload session has been swept by the reaper (cmd/apid/upload_session_reaper.go) and cannot be appended to or committed. The CLI is expected to detect this on the first PATCH/COMMIT after expiry and mint a fresh session (issue #1182 §P1 PR-2).`,
      },
    });
  }
}
