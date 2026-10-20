from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.dead_letter_events_response import DeadLetterEventsResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    limit: int | Unset = 20,
    before: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    params["before"] = before

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/dlq".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> DeadLetterEventsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = DeadLetterEventsResponse.from_dict(response.json())

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
) -> Response[DeadLetterEventsResponse | Problem]:
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
    limit: int | Unset = 20,
    before: str | Unset = UNSET,
) -> Response[DeadLetterEventsResponse | Problem]:
    """List dead-letter events for an app.

     Returns queue invocation, broker trigger, and outbound webhook
    delivery failures in one durable, newest-first ledger. The source row
    is not leased or mutated.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeadLetterEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        limit=limit,
        before=before,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    before: str | Unset = UNSET,
) -> DeadLetterEventsResponse | Problem | None:
    """List dead-letter events for an app.

     Returns queue invocation, broker trigger, and outbound webhook
    delivery failures in one durable, newest-first ledger. The source row
    is not leased or mutated.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeadLetterEventsResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        limit=limit,
        before=before,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    before: str | Unset = UNSET,
) -> Response[DeadLetterEventsResponse | Problem]:
    """List dead-letter events for an app.

     Returns queue invocation, broker trigger, and outbound webhook
    delivery failures in one durable, newest-first ledger. The source row
    is not leased or mutated.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DeadLetterEventsResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        limit=limit,
        before=before,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 20,
    before: str | Unset = UNSET,
) -> DeadLetterEventsResponse | Problem | None:
    """List dead-letter events for an app.

     Returns queue invocation, broker trigger, and outbound webhook
    delivery failures in one durable, newest-first ledger. The source row
    is not leased or mutated.

    Args:
        slug (str):
        limit (int | Unset):  Default: 20.
        before (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DeadLetterEventsResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            limit=limit,
            before=before,
        )
    ).parsed
