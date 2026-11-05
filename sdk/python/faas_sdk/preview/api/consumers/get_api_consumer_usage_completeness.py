import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_usage_completeness_response import APIConsumerUsageCompletenessResponse
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
    *,
    since: datetime.datetime,
    until: datetime.datetime,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since = since.isoformat()
    params["since"] = json_since

    json_until = until.isoformat()
    params["until"] = json_until

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/usage-completeness".format(
            slug=quote(str(slug), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerUsageCompletenessResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerUsageCompletenessResponse.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APIConsumerUsageCompletenessResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime,
    until: datetime.datetime,
) -> Response[APIConsumerUsageCompletenessResponse | Problem]:
    """Check one API consumer's billed usage against request telemetry.

     Compares successful requests in the billing ledger with successful
    requests in request telemetry, hour by hour, so usage the ledger
    never received is visible before a statement is invoiced. Only whole
    UTC hours that have settled (10 minutes) and that telemetry still
    retains (14 days) are checked. Telemetry is sampled, so it proves a
    lower bound of missing usage but cannot prove completeness of every
    request. Read-only; nothing is stored.

    Args:
        slug (str):
        consumer_id (UUID):
        since (datetime.datetime):
        until (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageCompletenessResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        since=since,
        until=until,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime,
    until: datetime.datetime,
) -> APIConsumerUsageCompletenessResponse | Problem | None:
    """Check one API consumer's billed usage against request telemetry.

     Compares successful requests in the billing ledger with successful
    requests in request telemetry, hour by hour, so usage the ledger
    never received is visible before a statement is invoiced. Only whole
    UTC hours that have settled (10 minutes) and that telemetry still
    retains (14 days) are checked. Telemetry is sampled, so it proves a
    lower bound of missing usage but cannot prove completeness of every
    request. Read-only; nothing is stored.

    Args:
        slug (str):
        consumer_id (UUID):
        since (datetime.datetime):
        until (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageCompletenessResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        consumer_id=consumer_id,
        client=client,
        since=since,
        until=until,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime,
    until: datetime.datetime,
) -> Response[APIConsumerUsageCompletenessResponse | Problem]:
    """Check one API consumer's billed usage against request telemetry.

     Compares successful requests in the billing ledger with successful
    requests in request telemetry, hour by hour, so usage the ledger
    never received is visible before a statement is invoiced. Only whole
    UTC hours that have settled (10 minutes) and that telemetry still
    retains (14 days) are checked. Telemetry is sampled, so it proves a
    lower bound of missing usage but cannot prove completeness of every
    request. Read-only; nothing is stored.

    Args:
        slug (str):
        consumer_id (UUID):
        since (datetime.datetime):
        until (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageCompletenessResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
        since=since,
        until=until,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
    since: datetime.datetime,
    until: datetime.datetime,
) -> APIConsumerUsageCompletenessResponse | Problem | None:
    """Check one API consumer's billed usage against request telemetry.

     Compares successful requests in the billing ledger with successful
    requests in request telemetry, hour by hour, so usage the ledger
    never received is visible before a statement is invoiced. Only whole
    UTC hours that have settled (10 minutes) and that telemetry still
    retains (14 days) are checked. Telemetry is sampled, so it proves a
    lower bound of missing usage but cannot prove completeness of every
    request. Read-only; nothing is stored.

    Args:
        slug (str):
        consumer_id (UUID):
        since (datetime.datetime):
        until (datetime.datetime):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageCompletenessResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            consumer_id=consumer_id,
            client=client,
            since=since,
            until=until,
        )
    ).parsed
