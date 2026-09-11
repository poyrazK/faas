/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AddTenantHostnameRequest } from '../models/AddTenantHostnameRequest.js';
import type { CreateTenantSurfaceRequest } from '../models/CreateTenantSurfaceRequest.js';
import type { ListTenantSurfacesResponse } from '../models/ListTenantSurfacesResponse.js';
import type { TenantHostnameResponse } from '../models/TenantHostnameResponse.js';
import type { TenantSurfaceResponse } from '../models/TenantSurfaceResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class TenantSurfacesService {
  /**
   * List tenant surfaces on an app.
   * Returns every active tenant surface on the app. Soft-deleted
   * surfaces are filtered out server-side. Returns 503 when the
   * `FAAS_TENANT_SURFACES_ENABLED` flag is off (the cluster ships
   * dark until the cert-engine real-mint ADR lands).
   *
   * @returns ListTenantSurfacesResponse The active surfaces on the app.
   * @throws ApiError
   */
  public static listTenantSurfaces({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListTenantSurfacesResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/tenant-surfaces',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: tenant_surfaces_not_allowed — this account's plan does not include tenant surfaces.`,
        404: `code: not_found`,
        503: `code: tenant_surfaces_not_enabled — the cluster operator has not enabled the tenant-surface API.`,
      },
    });
  }
  /**
   * Add a tenant surface with seed hostnames.
   * The customer-facing surface for issue #879 / ADR-100. One
   * surface holds N hostnames under one SAN bundle. Returns 202
   * (the cert engine has to mint; the surface is in pending/active
   * state).
   *
   * @returns TenantSurfaceResponse The new surface (pending active).
   * @throws ApiError
   */
  public static createTenantSurface({
    slug,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateTenantSurfaceRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<TenantSurfaceResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/tenant-surfaces',
      path: {
        'slug': slug,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: tenant_surfaces_not_allowed — this account's plan does not include tenant surfaces.`,
        403: `code: tenant_surface_quota | tenant_hostname_quota | forbidden — the account or seed-hostname cap is exhausted, or the caller lacks scope.`,
        404: `code: not_found`,
        409: `code: tenant_hostname_already_claimed | conflict — a seed hostname or surface name is already claimed.`,
        503: `code: tenant_surfaces_not_enabled — the cluster operator has not enabled the tenant-surface API.`,
      },
    });
  }
  /**
   * Get one tenant surface.
   * @returns TenantSurfaceResponse The surface + its hostnames.
   * @throws ApiError
   */
  public static getTenantSurface({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * The tenant surface id.
     */
    id: string,
  }): CancelablePromise<TenantSurfaceResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/tenant-surfaces/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: tenant_surfaces_not_allowed — this account's plan does not include tenant surfaces.`,
        404: `code: not_found`,
        503: `code: tenant_surfaces_not_enabled — the cluster operator has not enabled the tenant-surface API.`,
      },
    });
  }
  /**
   * Remove a tenant surface (soft-delete + cascade hostnames).
   * @returns void
   * @throws ApiError
   */
  public static deleteTenantSurface({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * The tenant surface id.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/tenant-surfaces/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: tenant_surfaces_not_allowed — this account's plan does not include tenant surfaces.`,
        404: `code: not_found`,
        503: `code: tenant_surfaces_not_enabled — the cluster operator has not enabled the tenant-surface API.`,
      },
    });
  }
  /**
   * Add a hostname to an existing surface.
   * @returns TenantHostnameResponse The hostname (pending DNS-01 verification).
   * @throws ApiError
   */
  public static addTenantHostname({
    slug,
    id,
    requestBody,
    idempotencyKey,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * The tenant surface id this hostname is being added to.
     */
    id: string,
    requestBody: AddTenantHostnameRequest,
    /**
     * Idempotency key for the POST. Stored for 24h. On replay the server
     * returns the original response with `Idempotent-Replayed: true`.
     *
     */
    idempotencyKey?: string,
  }): CancelablePromise<TenantHostnameResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/tenant-surfaces/{id}/hostnames',
      path: {
        'slug': slug,
        'id': id,
      },
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        401: `code: unauthorized`,
        402: `code: tenant_surfaces_not_allowed — this account's plan does not include tenant surfaces.`,
        403: `code: tenant_hostname_quota | forbidden — the surface hostname cap is exhausted or the caller lacks scope.`,
        404: `code: not_found`,
        409: `code: tenant_hostname_already_claimed | conflict — the hostname belongs to another tenant surface.`,
        503: `code: tenant_surfaces_not_enabled — the cluster operator has not enabled the tenant-surface API.`,
      },
    });
  }
  /**
   * Remove a hostname from a surface.
   * @returns void
   * @throws ApiError
   */
  public static removeTenantHostname({
    slug,
    id,
    hostname,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * The tenant surface id this hostname is being removed from.
     */
    id: string,
    /**
     * The hostname (lowercased canonical form).
     */
    hostname: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/apps/{slug}/tenant-surfaces/{id}/hostnames/{hostname}',
      path: {
        'slug': slug,
        'id': id,
        'hostname': hostname,
      },
      errors: {
        401: `code: unauthorized`,
        402: `code: tenant_surfaces_not_allowed — this account's plan does not include tenant surfaces.`,
        404: `code: not_found`,
        503: `code: tenant_surfaces_not_enabled — the cluster operator has not enabled the tenant-surface API.`,
      },
    });
  }
}
