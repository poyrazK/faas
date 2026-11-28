from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.list_schedule_occurrences_response import ListScheduleOccurrencesResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    id: str,
    *,
    limit: int | Unset = 50,
    before: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    json_before: str | Unset = UNSET
    if not isinstance(before, Unset):
        json_before = str(before)
    params["before"] = json_before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/crons/{id}/occurrences".format(
            id=quote(str(id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ListScheduleOccurrencesResponse | Problem | None:
    if response.status_code == 200:
        response_200 = ListScheduleOccurrencesResponse.from_dict(response.json())

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
) -> Response[ListScheduleOccurrencesResponse | Problem]:
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
    limit: int | Unset = 50,
    before: UUID | Unset = UNSET,
) -> Response[ListScheduleOccurrencesResponse | Problem]:
    """List durable scheduled occurrence decisions for a cron.

     Shows whether each nominal fire ran, was skipped, missed its start deadline, or was coalesced.

    Args:
        id (str):
        limit (int | Unset):  Default: 50.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListScheduleOccurrencesResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        limit=limit,
        before=before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: UUID | Unset = UNSET,
) -> ListScheduleOccurrencesResponse | Problem | None:
    """List durable scheduled occurrence decisions for a cron.

     Shows whether each nominal fire ran, was skipped, missed its start deadline, or was coalesced.

    Args:
        id (str):
        limit (int | Unset):  Default: 50.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListScheduleOccurrencesResponse | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        limit=limit,
        before=before,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: UUID | Unset = UNSET,
) -> Response[ListScheduleOccurrencesResponse | Problem]:
    """List durable scheduled occurrence decisions for a cron.

     Shows whether each nominal fire ran, was skipped, missed its start deadline, or was coalesced.

    Args:
        id (str):
        limit (int | Unset):  Default: 50.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ListScheduleOccurrencesResponse | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        limit=limit,
        before=before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 50,
    before: UUID | Unset = UNSET,
) -> ListScheduleOccurrencesResponse | Problem | None:
    """List durable scheduled occurrence decisions for a cron.

     Shows whether each nominal fire ran, was skipped, missed its start deadline, or was coalesced.

    Args:
        id (str):
        limit (int | Unset):  Default: 50.
        before (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ListScheduleOccurrencesResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            limit=limit,
            before=before,
        )
    ).parsed
