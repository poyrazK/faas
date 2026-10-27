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
   * Cookie-session-authenticated (NOT API-key). Mirrors the
   * embed.FS in cmd/gregale/templates/ via cmd/gregale/templates.Names
   * without importing the CLI's main package — the dashboard and
   * the CLI read the same 13-entry list through independent paths.
   * Adding a template means a new entry in cmd/gregale/templates/embed.go
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
