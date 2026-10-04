from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.app_env_export_response import AppEnvExportResponse
from ...models.export_app_env_request import ExportAppEnvRequest
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: ExportAppEnvRequest,
    scope: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/env-export".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AppEnvExportResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AppEnvExportResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AppEnvExportResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExportAppEnvRequest,
    scope: str | Unset = UNSET,
) -> Response[AppEnvExportResponse | Problem]:
    """Explicitly export plaintext environment values from one app scope.

     Requires admin or env:write and the session MFA gate. The body must
    acknowledge that downloaded values may be sensitive. Returns only
    mutable plaintext app_envs in one scope; never reads sealed secrets,
    deployment manifests, or image defaults. Omitted scope means default;
    __all__ is rejected. Metadata GET responses continue to omit values.
    Responses use Cache-Control no-store. This endpoint never persists
    an idempotency response containing plaintext. Audit records contain
    app ID, scope and count only, never values.

    Args:
        slug (str):
        scope (str | Unset):
        body (ExportAppEnvRequest): Explicit acknowledgement required before downloading
            potentially sensitive plaintext env values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppEnvExportResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExportAppEnvRequest,
    scope: str | Unset = UNSET,
) -> AppEnvExportResponse | Problem | None:
    """Explicitly export plaintext environment values from one app scope.

     Requires admin or env:write and the session MFA gate. The body must
    acknowledge that downloaded values may be sensitive. Returns only
    mutable plaintext app_envs in one scope; never reads sealed secrets,
    deployment manifests, or image defaults. Omitted scope means default;
    __all__ is rejected. Metadata GET responses continue to omit values.
    Responses use Cache-Control no-store. This endpoint never persists
    an idempotency response containing plaintext. Audit records contain
    app ID, scope and count only, never values.

    Args:
        slug (str):
        scope (str | Unset):
        body (ExportAppEnvRequest): Explicit acknowledgement required before downloading
            potentially sensitive plaintext env values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppEnvExportResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExportAppEnvRequest,
    scope: str | Unset = UNSET,
) -> Response[AppEnvExportResponse | Problem]:
    """Explicitly export plaintext environment values from one app scope.

     Requires admin or env:write and the session MFA gate. The body must
    acknowledge that downloaded values may be sensitive. Returns only
    mutable plaintext app_envs in one scope; never reads sealed secrets,
    deployment manifests, or image defaults. Omitted scope means default;
    __all__ is rejected. Metadata GET responses continue to omit values.
    Responses use Cache-Control no-store. This endpoint never persists
    an idempotency response containing plaintext. Audit records contain
    app ID, scope and count only, never values.

    Args:
        slug (str):
        scope (str | Unset):
        body (ExportAppEnvRequest): Explicit acknowledgement required before downloading
            potentially sensitive plaintext env values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AppEnvExportResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ExportAppEnvRequest,
    scope: str | Unset = UNSET,
) -> AppEnvExportResponse | Problem | None:
    """Explicitly export plaintext environment values from one app scope.

     Requires admin or env:write and the session MFA gate. The body must
    acknowledge that downloaded values may be sensitive. Returns only
    mutable plaintext app_envs in one scope; never reads sealed secrets,
    deployment manifests, or image defaults. Omitted scope means default;
    __all__ is rejected. Metadata GET responses continue to omit values.
    Responses use Cache-Control no-store. This endpoint never persists
    an idempotency response containing plaintext. Audit records contain
    app ID, scope and count only, never values.

    Args:
        slug (str):
        scope (str | Unset):
        body (ExportAppEnvRequest): Explicit acknowledgement required before downloading
            potentially sensitive plaintext env values.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AppEnvExportResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            scope=scope,
        )
    ).parsed
