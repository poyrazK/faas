from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.managed_realtime_access_request import ManagedRealtimeAccessRequest
from ...models.managed_realtime_access_response import ManagedRealtimeAccessResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    id: str,
    principal: str,
    *,
    body: ManagedRealtimeAccessRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/realtime/endpoints/{id}/principals/{principal}/access".format(
            slug=quote(str(slug), safe=""),
            id=quote(str(id), safe=""),
            principal=quote(str(principal), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedRealtimeAccessResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ManagedRealtimeAccessResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 402:
        response_402 = Problem.from_dict(response.json())

        return response_402

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
) -> Response[ManagedRealtimeAccessResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    id: str,
    principal: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeAccessRequest,
) -> Response[ManagedRealtimeAccessResponse | Problem]:
    """Revoke channel access or older sessions, or remove a channel deny.

     Requires MFA and write scope. OIDC operator preview must be enabled on apid and every realtime node.
    Channel revocation also denies its private activity scopes. Revoke_sessions records a monotonic
    server-time cutoff; tokens require an integer iat strictly after it. Nodes refresh access state
    every second and fail closed on refresh failure. Restoration does not grant app membership or
    automatically resubscribe. Success confirms persistence, not instantaneous fleet convergence.

    Args:
        slug (str):
        id (str):
        principal (str):
        body (ManagedRealtimeAccessRequest): Managed Realtime Access Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeAccessResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        principal=principal,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    id: str,
    principal: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeAccessRequest,
) -> ManagedRealtimeAccessResponse | Problem | None:
    """Revoke channel access or older sessions, or remove a channel deny.

     Requires MFA and write scope. OIDC operator preview must be enabled on apid and every realtime node.
    Channel revocation also denies its private activity scopes. Revoke_sessions records a monotonic
    server-time cutoff; tokens require an integer iat strictly after it. Nodes refresh access state
    every second and fail closed on refresh failure. Restoration does not grant app membership or
    automatically resubscribe. Success confirms persistence, not instantaneous fleet convergence.

    Args:
        slug (str):
        id (str):
        principal (str):
        body (ManagedRealtimeAccessRequest): Managed Realtime Access Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeAccessResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        id=id,
        principal=principal,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    id: str,
    principal: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeAccessRequest,
) -> Response[ManagedRealtimeAccessResponse | Problem]:
    """Revoke channel access or older sessions, or remove a channel deny.

     Requires MFA and write scope. OIDC operator preview must be enabled on apid and every realtime node.
    Channel revocation also denies its private activity scopes. Revoke_sessions records a monotonic
    server-time cutoff; tokens require an integer iat strictly after it. Nodes refresh access state
    every second and fail closed on refresh failure. Restoration does not grant app membership or
    automatically resubscribe. Success confirms persistence, not instantaneous fleet convergence.

    Args:
        slug (str):
        id (str):
        principal (str):
        body (ManagedRealtimeAccessRequest): Managed Realtime Access Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedRealtimeAccessResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        id=id,
        principal=principal,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    id: str,
    principal: str,
    *,
    client: AuthenticatedClient | Client,
    body: ManagedRealtimeAccessRequest,
) -> ManagedRealtimeAccessResponse | Problem | None:
    """Revoke channel access or older sessions, or remove a channel deny.

     Requires MFA and write scope. OIDC operator preview must be enabled on apid and every realtime node.
    Channel revocation also denies its private activity scopes. Revoke_sessions records a monotonic
    server-time cutoff; tokens require an integer iat strictly after it. Nodes refresh access state
    every second and fail closed on refresh failure. Restoration does not grant app membership or
    automatically resubscribe. Success confirms persistence, not instantaneous fleet convergence.

    Args:
        slug (str):
        id (str):
        principal (str):
        body (ManagedRealtimeAccessRequest): Managed Realtime Access Request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedRealtimeAccessResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            id=id,
            principal=principal,
            client=client,
            body=body,
        )
    ).parsed
