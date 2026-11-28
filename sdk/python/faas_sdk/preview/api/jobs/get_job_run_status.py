from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...models.resource_status import ResourceStatus
from ...types import Response


def _get_kwargs(
    name: str,
    id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/jobs/{name}/runs/{id}/status".format(
            name=quote(str(name), safe=""),
            id=quote(str(id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Problem | ResourceStatus:
    if response.status_code == 200:
        response_200 = ResourceStatus.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | ResourceStatus]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ResourceStatus]:
    """Read a job run's conditions.

     Preview (resource-conditions capability). The run's Succeeded and
    Progressing conditions, mapped from the response
    GET /v1/jobs/{name}/runs/{id} returns and never stored. A job run has
    no Ready condition: it runs to completion. Failed, cancelled and
    dead-letter runs report terminal conditions. Cache-Control no-store.

    Args:
        name (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ResourceStatus]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ResourceStatus | None:
    """Read a job run's conditions.

     Preview (resource-conditions capability). The run's Succeeded and
    Progressing conditions, mapped from the response
    GET /v1/jobs/{name}/runs/{id} returns and never stored. A job run has
    no Ready condition: it runs to completion. Failed, cancelled and
    dead-letter runs report terminal conditions. Cache-Control no-store.

    Args:
        name (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ResourceStatus
    """

    return sync_detailed(
        name=name,
        id=id,
        client=client,
    ).parsed


async def asyncio_detailed(
    name: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Problem | ResourceStatus]:
    """Read a job run's conditions.

     Preview (resource-conditions capability). The run's Succeeded and
    Progressing conditions, mapped from the response
    GET /v1/jobs/{name}/runs/{id} returns and never stored. A job run has
    no Ready condition: it runs to completion. Failed, cancelled and
    dead-letter runs report terminal conditions. Cache-Control no-store.

    Args:
        name (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | ResourceStatus]
    """

    kwargs = _get_kwargs(
        name=name,
        id=id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Problem | ResourceStatus | None:
    """Read a job run's conditions.

     Preview (resource-conditions capability). The run's Succeeded and
    Progressing conditions, mapped from the response
    GET /v1/jobs/{name}/runs/{id} returns and never stored. A job run has
    no Ready condition: it runs to completion. Failed, cancelled and
    dead-letter runs report terminal conditions. Cache-Control no-store.

    Args:
        name (str):
        id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | ResourceStatus
    """

    return (
        await asyncio_detailed(
            name=name,
            id=id,
            client=client,
        )
    ).parsed
