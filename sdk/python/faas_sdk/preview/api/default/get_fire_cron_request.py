from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.fire_cron_request_response import FireCronRequestResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    request_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/cron-fire-now-requests/{request_id}".format(
            request_id=quote(str(request_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> FireCronRequestResponse | Problem | None:
    if response.status_code == 200:
        response_200 = FireCronRequestResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

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
) -> Response[FireCronRequestResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[FireCronRequestResponse | Problem]:
    """Get fire-now request state

     Polling surface for the row that `POST /v1/crons/{id}/run`
    inserted (issue #791 PR-D / ADR-090 §Sub-decision 7).

    Args:
        request_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FireCronRequestResponse | Problem]
    """

    kwargs = _get_kwargs(
        request_id=request_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> FireCronRequestResponse | Problem | None:
    """Get fire-now request state

     Polling surface for the row that `POST /v1/crons/{id}/run`
    inserted (issue #791 PR-D / ADR-090 §Sub-decision 7).

    Args:
        request_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FireCronRequestResponse | Problem
    """

    return sync_detailed(
        request_id=request_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[FireCronRequestResponse | Problem]:
    """Get fire-now request state

     Polling surface for the row that `POST /v1/crons/{id}/run`
    inserted (issue #791 PR-D / ADR-090 §Sub-decision 7).

    Args:
        request_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[FireCronRequestResponse | Problem]
    """

    kwargs = _get_kwargs(
        request_id=request_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    request_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> FireCronRequestResponse | Problem | None:
    """Get fire-now request state

     Polling surface for the row that `POST /v1/crons/{id}/run`
    inserted (issue #791 PR-D / ADR-090 §Sub-decision 7).

    Args:
        request_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        FireCronRequestResponse | Problem
    """

    return (
        await asyncio_detailed(
            request_id=request_id,
            client=client,
        )
    ).parsed
