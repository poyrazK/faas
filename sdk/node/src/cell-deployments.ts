import type { CancelablePromise } from './generated/core/CancelablePromise.js';
import { OpenAPI } from './generated/core/OpenAPI.js';
import { request } from './generated/core/request.js';
import type { DeploymentResponse } from './generated/models/DeploymentResponse.js';

/** Source form for the immutable regional deployment endpoint (ADR-603). */
export interface CellDeploymentSourceForm {
  source: Blob;
  dockerfile?: boolean;
  runtime?: string;
  handler?: string;
  source_root?: string;
  scope?: string;
  source_url?: string;
  commit_sha?: string;
  healthcheck?: string;
  workflows?: string;
}

/** Upload regional deployment intent with a cell-issued key configured on FaaSClient.
 * The generated transport refuses redirects for this exact UUID-addressed route.
 */
export function deployCellAppMultipart({ cellId, appId, formData, idempotencyKey }: {
  cellId: string;
  appId: string;
  formData: CellDeploymentSourceForm;
  idempotencyKey?: string;
}): CancelablePromise<DeploymentResponse> {
  return request(OpenAPI, {
    method: 'POST',
    url: '/v1/cells/{cell_id}/apps/{app_id}/deployments',
    path: { cell_id: cellId, app_id: appId },
    formData,
    headers: { 'Idempotency-Key': idempotencyKey },
  });
}
