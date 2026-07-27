/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class MetaService {
  /**
   * This spec, as YAML.
   * Machine-readable API description. Same bytes as
   * `api/openapi.yaml` at the repo HEAD; embedded at build time
   * via `//go:embed` in `pkg/apid/openapi_handler.go`. No auth.
   *
   * @returns string The OpenAPI 3.1 document for this API.
   * @throws ApiError
   */
  public static getOpenApiSpec(): CancelablePromise<string> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/openapi.yaml',
    });
  }
  /**
   * This spec, as JSON.
   * Same spec, YAML parsed and re-emitted as JSON. Preferred by
   * SDK generators (`openapi-generator`, `oapi-codegen`).
   *
   * @returns any The OpenAPI 3.1 document for this API, as JSON.
   * @throws ApiError
   */
  public static getOpenApiSpecJson(): CancelablePromise<Record<string, any>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/openapi.json',
    });
  }
}
