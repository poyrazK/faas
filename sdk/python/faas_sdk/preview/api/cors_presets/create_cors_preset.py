from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.cors_preset_response import CorsPresetResponse
from ...models.create_cors_preset_request import CreateCorsPresetRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    body: CreateCorsPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/cors-presets",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CorsPresetResponse | Problem | None:
    if response.status_code == 201:
        response_201 = CorsPresetResponse.from_dict(response.json())

        return response_201

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CorsPresetResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateCorsPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[CorsPresetResponse | Problem]:
    r"""Create a CORS preset.

     Creates a new cors_presets row owned by the caller's
    account. AppID is optional (null = account-wide; UUID =
    app-scoped). The body validation mirrors the storage-
    side CHECK constraints: name 1..64, max_age 0..86400,
    at-least-one allow_origin + allow_method. The
    *+credentials footgun (ADR-091 D12) returns 422
    cors_wildcard_with_credentials when the create body
    combines AllowCredentials: true with AllowOrigins:
    [\"*\"].

    Pre-loadApp gates fire in this order: 402
    plan_cors_preset_not_allowed on the Free-tier cap-0 →
    422 cors_preset_invalid on the body shape → 404
    cors_preset_app_not_found on a cross-tenant app_id →
    403 plan_cors_preset_quota_reached on the per-account
    / per-app cap → 409 cors_preset_name_conflict on a
    duplicate (account_id, COALESCE(app_id, '00..00'),
    name) tuple.

    Args:
        idempotency_key (str | Unset):
        body (CreateCorsPresetRequest): Body for POST /v1/cors-presets. The customer must supply
            at least one allow_origin and one allow_method. AppID is
            optional on the wire (null pointer = "account-wide",
            non-nil = "app-scoped"); the handler maps the
            pointer-nil case to a SQL NULL on insert. Name length
            is 1..64 characters (cors_presets_name_check). The
            *+credentials footgun (ADR-091 D12) is enforced at
            validate-time.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CorsPresetResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: CreateCorsPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> CorsPresetResponse | Problem | None:
    r"""Create a CORS preset.

     Creates a new cors_presets row owned by the caller's
    account. AppID is optional (null = account-wide; UUID =
    app-scoped). The body validation mirrors the storage-
    side CHECK constraints: name 1..64, max_age 0..86400,
    at-least-one allow_origin + allow_method. The
    *+credentials footgun (ADR-091 D12) returns 422
    cors_wildcard_with_credentials when the create body
    combines AllowCredentials: true with AllowOrigins:
    [\"*\"].

    Pre-loadApp gates fire in this order: 402
    plan_cors_preset_not_allowed on the Free-tier cap-0 →
    422 cors_preset_invalid on the body shape → 404
    cors_preset_app_not_found on a cross-tenant app_id →
    403 plan_cors_preset_quota_reached on the per-account
    / per-app cap → 409 cors_preset_name_conflict on a
    duplicate (account_id, COALESCE(app_id, '00..00'),
    name) tuple.

    Args:
        idempotency_key (str | Unset):
        body (CreateCorsPresetRequest): Body for POST /v1/cors-presets. The customer must supply
            at least one allow_origin and one allow_method. AppID is
            optional on the wire (null pointer = "account-wide",
            non-nil = "app-scoped"); the handler maps the
            pointer-nil case to a SQL NULL on insert. Name length
            is 1..64 characters (cors_presets_name_check). The
            *+credentials footgun (ADR-091 D12) is enforced at
            validate-time.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CorsPresetResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CreateCorsPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[CorsPresetResponse | Problem]:
    r"""Create a CORS preset.

     Creates a new cors_presets row owned by the caller's
    account. AppID is optional (null = account-wide; UUID =
    app-scoped). The body validation mirrors the storage-
    side CHECK constraints: name 1..64, max_age 0..86400,
    at-least-one allow_origin + allow_method. The
    *+credentials footgun (ADR-091 D12) returns 422
    cors_wildcard_with_credentials when the create body
    combines AllowCredentials: true with AllowOrigins:
    [\"*\"].

    Pre-loadApp gates fire in this order: 402
    plan_cors_preset_not_allowed on the Free-tier cap-0 →
    422 cors_preset_invalid on the body shape → 404
    cors_preset_app_not_found on a cross-tenant app_id →
    403 plan_cors_preset_quota_reached on the per-account
    / per-app cap → 409 cors_preset_name_conflict on a
    duplicate (account_id, COALESCE(app_id, '00..00'),
    name) tuple.

    Args:
        idempotency_key (str | Unset):
        body (CreateCorsPresetRequest): Body for POST /v1/cors-presets. The customer must supply
            at least one allow_origin and one allow_method. AppID is
            optional on the wire (null pointer = "account-wide",
            non-nil = "app-scoped"); the handler maps the
            pointer-nil case to a SQL NULL on insert. Name length
            is 1..64 characters (cors_presets_name_check). The
            *+credentials footgun (ADR-091 D12) is enforced at
            validate-time.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CorsPresetResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CreateCorsPresetRequest,
    idempotency_key: str | Unset = UNSET,
) -> CorsPresetResponse | Problem | None:
    r"""Create a CORS preset.

     Creates a new cors_presets row owned by the caller's
    account. AppID is optional (null = account-wide; UUID =
    app-scoped). The body validation mirrors the storage-
    side CHECK constraints: name 1..64, max_age 0..86400,
    at-least-one allow_origin + allow_method. The
    *+credentials footgun (ADR-091 D12) returns 422
    cors_wildcard_with_credentials when the create body
    combines AllowCredentials: true with AllowOrigins:
    [\"*\"].

    Pre-loadApp gates fire in this order: 402
    plan_cors_preset_not_allowed on the Free-tier cap-0 →
    422 cors_preset_invalid on the body shape → 404
    cors_preset_app_not_found on a cross-tenant app_id →
    403 plan_cors_preset_quota_reached on the per-account
    / per-app cap → 409 cors_preset_name_conflict on a
    duplicate (account_id, COALESCE(app_id, '00..00'),
    name) tuple.

    Args:
        idempotency_key (str | Unset):
        body (CreateCorsPresetRequest): Body for POST /v1/cors-presets. The customer must supply
            at least one allow_origin and one allow_method. AppID is
            optional on the wire (null pointer = "account-wide",
            non-nil = "app-scoped"); the handler maps the
            pointer-nil case to a SQL NULL on insert. Name length
            is 1..64 characters (cors_presets_name_check). The
            *+credentials footgun (ADR-091 D12) is enforced at
            validate-time.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CorsPresetResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
