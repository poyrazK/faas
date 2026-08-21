/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * JSON body for POST /v1/apps/{slug}/deployments/source-ref
 * (DEPLOY-PROV-4 / ADR-092, issue #739). The headless CI deploy
 * path: `repo` resolves to an install-token-bound fetch, `ref` is
 * the customer's chosen input (branch / tag / SHA — server
 * resolves to a 40-char SHA before stamping the deployment row).
 *
 */
export type SourceRefDeployRequest = {
  /**
   * GitHub owner/name slug, e.g. `onebox-faas/hello`.
   */
  repo: string;
  /**
   * Commit ref — 40-char SHA, branch, or tag. api.github.com
   * /repos/<repo>/commits/<ref> resolves branch / tag inputs
   * to a pinned SHA before the tarball fetch starts. The wire
   * shape pins to the resolved 40-char SHA (server override;
   * caller's `ref` is preserved on the `deploy.source_ref`
   * audit row for traceability).
   *
   */
  ref: string;
  /**
   * Forward-compat field. PR-A only supports `tarball`.
   */
  format?: 'tarball';
};

