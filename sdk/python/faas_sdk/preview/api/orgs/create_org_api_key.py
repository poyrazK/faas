from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_key_response import APIKeyResponse
from ...models.create_org_api_key_request import CreateOrgAPIKeyRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateOrgAPIKeyRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/orgs/{slug}/keys".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIKeyResponse | Problem | None:
    if response.status_code == 201:
        response_201 = APIKeyResponse.from_dict(response.json())

        return response_201

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

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIKeyResponse | Problem]:
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
    body: CreateOrgAPIKeyRequest,
) -> Response[APIKeyResponse | Problem]:
    """Mint a new API key for the active org.

     Returns the plaintext exactly once (same as `POST /v1/keys`).
    The new row's `org_id` is the loaded membership's org; personal
    orgs are mintable (the `org_personal_immutable` 409 applies to
    mutations on the org row, not key mints against it).

    Args:
        slug (str):
        body (CreateOrgAPIKeyRequest): POST /v1/orgs/{slug}/keys body. Mirrors `CreateKeyRequest`
            (PR 6 keeps them in lockstep) — label + optional scopes. Empty `scopes` defaults to
            `["admin"]` so existing callers preserve the legacy full-access behavior. See IAM-1,
            ADR-034 rev2.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIKeyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOrgAPIKeyRequest,
) -> APIKeyResponse | Problem | None:
    """Mint a new API key for the active org.

     Returns the plaintext exactly once (same as `POST /v1/keys`).
    The new row's `org_id` is the loaded membership's org; personal
    orgs are mintable (the `org_personal_immutable` 409 applies to
    mutations on the org row, not key mints against it).

    Args:
        slug (str):
        body (CreateOrgAPIKeyRequest): POST /v1/orgs/{slug}/keys body. Mirrors `CreateKeyRequest`
            (PR 6 keeps them in lockstep) — label + optional scopes. Empty `scopes` defaults to
            `["admin"]` so existing callers preserve the legacy full-access behavior. See IAM-1,
            ADR-034 rev2.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIKeyResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOrgAPIKeyRequest,
) -> Response[APIKeyResponse | Problem]:
    """Mint a new API key for the active org.

     Returns the plaintext exactly once (same as `POST /v1/keys`).
    The new row's `org_id` is the loaded membership's org; personal
    orgs are mintable (the `org_personal_immutable` 409 applies to
    mutations on the org row, not key mints against it).

    Args:
        slug (str):
        body (CreateOrgAPIKeyRequest): POST /v1/orgs/{slug}/keys body. Mirrors `CreateKeyRequest`
            (PR 6 keeps them in lockstep) — label + optional scopes. Empty `scopes` defaults to
            `["admin"]` so existing callers preserve the legacy full-access behavior. See IAM-1,
            ADR-034 rev2.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIKeyResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateOrgAPIKeyRequest,
) -> APIKeyResponse | Problem | None:
    """Mint a new API key for the active org.

     Returns the plaintext exactly once (same as `POST /v1/keys`).
    The new row's `org_id` is the loaded membership's org; personal
    orgs are mintable (the `org_personal_immutable` 409 applies to
    mutations on the org row, not key mints against it).

    Args:
        slug (str):
        body (CreateOrgAPIKeyRequest): POST /v1/orgs/{slug}/keys body. Mirrors `CreateKeyRequest`
            (PR 6 keeps them in lockstep) — label + optional scopes. Empty `scopes` defaults to
            `["admin"]` so existing callers preserve the legacy full-access behavior. See IAM-1,
            ADR-034 rev2.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIKeyResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
