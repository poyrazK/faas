from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.cors_preset_response import CorsPresetResponse
from ...models.problem import Problem
from ...models.update_cors_preset_request import UpdateCorsPresetRequest
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: UpdateCorsPresetRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "patch",
        "url": "/v1/cors-presets/{id}".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CorsPresetResponse | Problem | None:
    if response.status_code == 200:
        response_200 = CorsPresetResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateCorsPresetRequest,
) -> Response[CorsPresetResponse | Problem]:
    r"""Partial-update a CORS preset.

     Applies the partial-update (nil-skip convention) to
    the cors_presets row. The PATCH body must include at
    least one field (an empty body returns 422
    cors_preset_update_requires_field). The wire-level
    Validate enforces the same partial grammar as Create
    (CorsOriginPattern on allow_origins if provided,
    non-empty allow_methods if provided, max_age bound
    0..86400). The handler additionally re-validates the
    post-update merged shape against the *+credentials
    footgun (a PATCH that flips AllowCredentials to true
    while leaving AllowOrigins=[\"*\"] is rejected).

    The pgstore trigger fires pg_notify
    ('cors_preset_changed', account_id) AFTER the UPDATE
    commits so the gatewayd-internal listener reloads
    the affected account's preset overlay (ADR-129 D4).

    Args:
        id (UUID):
        body (UpdateCorsPresetRequest): Body for PATCH /v1/cors-presets/{id}. Every field is
            optional (PATCH nil-skip convention). At least one field
            must be present (an empty PATCH is rejected with 422
            cors_preset_update_requires_field). The same partial
            grammar check that fires on CreateCorsPresetRequest
            (CorsOriginPattern, *+credentials footgun) fires here
            on the partial payload; the apid handler additionally
            re-validates against the merged post-update shape so a
            PATCH that flips AllowCredentials to true while leaving
            AllowOrigins=["*"] is rejected.

            app_id uses the **string tri-state: outer null = "do
            not touch", inner null = "set to NULL (account-wide)",
            inner non-null = "set to UUID (app-scoped)".

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CorsPresetResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateCorsPresetRequest,
) -> CorsPresetResponse | Problem | None:
    r"""Partial-update a CORS preset.

     Applies the partial-update (nil-skip convention) to
    the cors_presets row. The PATCH body must include at
    least one field (an empty body returns 422
    cors_preset_update_requires_field). The wire-level
    Validate enforces the same partial grammar as Create
    (CorsOriginPattern on allow_origins if provided,
    non-empty allow_methods if provided, max_age bound
    0..86400). The handler additionally re-validates the
    post-update merged shape against the *+credentials
    footgun (a PATCH that flips AllowCredentials to true
    while leaving AllowOrigins=[\"*\"] is rejected).

    The pgstore trigger fires pg_notify
    ('cors_preset_changed', account_id) AFTER the UPDATE
    commits so the gatewayd-internal listener reloads
    the affected account's preset overlay (ADR-129 D4).

    Args:
        id (UUID):
        body (UpdateCorsPresetRequest): Body for PATCH /v1/cors-presets/{id}. Every field is
            optional (PATCH nil-skip convention). At least one field
            must be present (an empty PATCH is rejected with 422
            cors_preset_update_requires_field). The same partial
            grammar check that fires on CreateCorsPresetRequest
            (CorsOriginPattern, *+credentials footgun) fires here
            on the partial payload; the apid handler additionally
            re-validates against the merged post-update shape so a
            PATCH that flips AllowCredentials to true while leaving
            AllowOrigins=["*"] is rejected.

            app_id uses the **string tri-state: outer null = "do
            not touch", inner null = "set to NULL (account-wide)",
            inner non-null = "set to UUID (app-scoped)".

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CorsPresetResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateCorsPresetRequest,
) -> Response[CorsPresetResponse | Problem]:
    r"""Partial-update a CORS preset.

     Applies the partial-update (nil-skip convention) to
    the cors_presets row. The PATCH body must include at
    least one field (an empty body returns 422
    cors_preset_update_requires_field). The wire-level
    Validate enforces the same partial grammar as Create
    (CorsOriginPattern on allow_origins if provided,
    non-empty allow_methods if provided, max_age bound
    0..86400). The handler additionally re-validates the
    post-update merged shape against the *+credentials
    footgun (a PATCH that flips AllowCredentials to true
    while leaving AllowOrigins=[\"*\"] is rejected).

    The pgstore trigger fires pg_notify
    ('cors_preset_changed', account_id) AFTER the UPDATE
    commits so the gatewayd-internal listener reloads
    the affected account's preset overlay (ADR-129 D4).

    Args:
        id (UUID):
        body (UpdateCorsPresetRequest): Body for PATCH /v1/cors-presets/{id}. Every field is
            optional (PATCH nil-skip convention). At least one field
            must be present (an empty PATCH is rejected with 422
            cors_preset_update_requires_field). The same partial
            grammar check that fires on CreateCorsPresetRequest
            (CorsOriginPattern, *+credentials footgun) fires here
            on the partial payload; the apid handler additionally
            re-validates against the merged post-update shape so a
            PATCH that flips AllowCredentials to true while leaving
            AllowOrigins=["*"] is rejected.

            app_id uses the **string tri-state: outer null = "do
            not touch", inner null = "set to NULL (account-wide)",
            inner non-null = "set to UUID (app-scoped)".

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CorsPresetResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: UpdateCorsPresetRequest,
) -> CorsPresetResponse | Problem | None:
    r"""Partial-update a CORS preset.

     Applies the partial-update (nil-skip convention) to
    the cors_presets row. The PATCH body must include at
    least one field (an empty body returns 422
    cors_preset_update_requires_field). The wire-level
    Validate enforces the same partial grammar as Create
    (CorsOriginPattern on allow_origins if provided,
    non-empty allow_methods if provided, max_age bound
    0..86400). The handler additionally re-validates the
    post-update merged shape against the *+credentials
    footgun (a PATCH that flips AllowCredentials to true
    while leaving AllowOrigins=[\"*\"] is rejected).

    The pgstore trigger fires pg_notify
    ('cors_preset_changed', account_id) AFTER the UPDATE
    commits so the gatewayd-internal listener reloads
    the affected account's preset overlay (ADR-129 D4).

    Args:
        id (UUID):
        body (UpdateCorsPresetRequest): Body for PATCH /v1/cors-presets/{id}. Every field is
            optional (PATCH nil-skip convention). At least one field
            must be present (an empty PATCH is rejected with 422
            cors_preset_update_requires_field). The same partial
            grammar check that fires on CreateCorsPresetRequest
            (CorsOriginPattern, *+credentials footgun) fires here
            on the partial payload; the apid handler additionally
            re-validates against the merged post-update shape so a
            PATCH that flips AllowCredentials to true while leaving
            AllowOrigins=["*"] is rejected.

            app_id uses the **string tri-state: outer null = "do
            not touch", inner null = "set to NULL (account-wide)",
            inner non-null = "set to UUID (app-scoped)".

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CorsPresetResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
