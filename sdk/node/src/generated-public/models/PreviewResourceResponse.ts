/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppResponse } from './AppResponse.js';
import type { DeploymentResponse } from './DeploymentResponse.js';
import type { PreviewProductionChangesResponse } from './PreviewProductionChangesResponse.js';
import type { PreviewResourceLinksResponse } from './PreviewResourceLinksResponse.js';
/**
 * A first-class preview with production comparison and observability links.
 */
export type PreviewResourceResponse = {
  app: AppResponse;
  parent?: AppResponse;
  latest_deployment?: DeploymentResponse;
  production_deployment?: DeploymentResponse;
  changes_from_production: PreviewProductionChangesResponse;
  links: PreviewResourceLinksResponse;
};

