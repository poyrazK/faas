from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.data_connection_check import DataConnectionCheck
from ...models.problem import Problem
from ...models.request_data_connection_check import RequestDataConnectionCheck
from ...types import Response


def _get_kwargs(
    id: UUID,
    *,
    body: RequestDataConnectionCheck,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/data/connections/{id}/check".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> DataConnectionCheck | Problem:
    if response.status_code == 202:
        response_202 = DataConnectionCheck.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DataConnectionCheck | Problem]:
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
    body: RequestDataConnectionCheck,
) -> Response[DataConnectionCheck | Problem]:
    """Queue a private worker connection check and table discovery

     Staging only. Coalesces active or very recent checks for the same credential revision and optional
    mirror. No source SQL runs in the HTTP handler. Results are compatibility observations, not a
    production qualification.

    Args:
        id (UUID):
        body (RequestDataConnectionCheck): Queue inspection of the current credential, optionally
            including a ready, empty managed mirror.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataConnectionCheck | Problem]
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
    body: RequestDataConnectionCheck,
) -> DataConnectionCheck | Problem | None:
    """Queue a private worker connection check and table discovery

     Staging only. Coalesces active or very recent checks for the same credential revision and optional
    mirror. No source SQL runs in the HTTP handler. Results are compatibility observations, not a
    production qualification.

    Args:
        id (UUID):
        body (RequestDataConnectionCheck): Queue inspection of the current credential, optionally
            including a ready, empty managed mirror.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataConnectionCheck | Problem
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
    body: RequestDataConnectionCheck,
) -> Response[DataConnectionCheck | Problem]:
    """Queue a private worker connection check and table discovery

     Staging only. Coalesces active or very recent checks for the same credential revision and optional
    mirror. No source SQL runs in the HTTP handler. Results are compatibility observations, not a
    production qualification.

    Args:
        id (UUID):
        body (RequestDataConnectionCheck): Queue inspection of the current credential, optionally
            including a ready, empty managed mirror.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DataConnectionCheck | Problem]
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
    body: RequestDataConnectionCheck,
) -> DataConnectionCheck | Problem | None:
    """Queue a private worker connection check and table discovery

     Staging only. Coalesces active or very recent checks for the same credential revision and optional
    mirror. No source SQL runs in the HTTP handler. Results are compatibility observations, not a
    production qualification.

    Args:
        id (UUID):
        body (RequestDataConnectionCheck): Queue inspection of the current credential, optionally
            including a ready, empty managed mirror.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DataConnectionCheck | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
