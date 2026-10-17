from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_live_queries import ManagedPostgresLiveQueries
from ...models.problem import Problem
from ...models.rotate_managed_postgres_live_secret_request import RotateManagedPostgresLiveSecretRequest
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    body: RotateManagedPostgresLiveSecretRequest | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/postgres/databases/{id}/live/rotate".format(
            id=quote(str(id), safe=""),
        ),
    }

    if not isinstance(body, Unset):
        _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresLiveQueries | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresLiveQueries.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresLiveQueries | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateManagedPostgresLiveSecretRequest | Unset = UNSET,
) -> Response[ManagedPostgresLiveQueries | Problem]:
    """Rotate a managed PostgreSQL live secret

     Issues a new live secret, returned once in this response. The previous secret keeps opening seals
    for overlap_seconds (default 24 hours, at most 7 days); 0 revokes it at once. Any older secret is
    removed, so a database holds at most two.

    Args:
        id (str):
        body (RotateManagedPostgresLiveSecretRequest | Unset): Live secret rotation. An empty body
            keeps the previous secret for 24 hours.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresLiveQueries | Problem]
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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateManagedPostgresLiveSecretRequest | Unset = UNSET,
) -> ManagedPostgresLiveQueries | Problem | None:
    """Rotate a managed PostgreSQL live secret

     Issues a new live secret, returned once in this response. The previous secret keeps opening seals
    for overlap_seconds (default 24 hours, at most 7 days); 0 revokes it at once. Any older secret is
    removed, so a database holds at most two.

    Args:
        id (str):
        body (RotateManagedPostgresLiveSecretRequest | Unset): Live secret rotation. An empty body
            keeps the previous secret for 24 hours.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresLiveQueries | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateManagedPostgresLiveSecretRequest | Unset = UNSET,
) -> Response[ManagedPostgresLiveQueries | Problem]:
    """Rotate a managed PostgreSQL live secret

     Issues a new live secret, returned once in this response. The previous secret keeps opening seals
    for overlap_seconds (default 24 hours, at most 7 days); 0 revokes it at once. Any older secret is
    removed, so a database holds at most two.

    Args:
        id (str):
        body (RotateManagedPostgresLiveSecretRequest | Unset): Live secret rotation. An empty body
            keeps the previous secret for 24 hours.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresLiveQueries | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: RotateManagedPostgresLiveSecretRequest | Unset = UNSET,
) -> ManagedPostgresLiveQueries | Problem | None:
    """Rotate a managed PostgreSQL live secret

     Issues a new live secret, returned once in this response. The previous secret keeps opening seals
    for overlap_seconds (default 24 hours, at most 7 days); 0 revokes it at once. Any older secret is
    removed, so a database holds at most two.

    Args:
        id (str):
        body (RotateManagedPostgresLiveSecretRequest | Unset): Live secret rotation. An empty body
            keeps the previous secret for 24 hours.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresLiveQueries | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
