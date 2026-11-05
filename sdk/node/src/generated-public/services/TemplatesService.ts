/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TemplateView } from '../models/TemplateView.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class TemplatesService {
  /**
   * Catalog of starter templates the dashboard wizard renders.
   * Cookie-session-authenticated (NOT API-key). The dashboard and
   * the CLI read the same template list.
   * Adding a template means a new entry
   * + the same category + description wiring here.
   *
   * Used by the dashboard's /dashboard/apps/new wizard to populate
   * the "Starting template" dropdown. The CLI's `gregale deploy
   * --template NAME` and `gregale init --template NAME` validators
   * reference the same source on the CLI side.
   *
   * @returns TemplateView Every available template with category + description.
   * @throws ApiError
   */
  public static listTemplates(): CancelablePromise<Array<TemplateView>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/templates',
      errors: {
        401: `code: unauthorized`,
      },
    });
  }
}
