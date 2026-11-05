import datetime
from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_usage_response import APIConsumerUsageResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
    *,
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_since: str | Unset = UNSET
    if not isinstance(since, Unset):
        json_since = since.isoformat()
    params["since"] = json_since

    json_until: str | Unset = UNSET
    if not isinstance(until, Unset):
        json_until = until.isoformat()
    params["until"] = json_until

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/usage".format(
            slug=quote(str(slug), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerUsageResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerUsageResponse.from_dict(response.json())

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
) -> Response[APIConsumerUsageResponse | Problem]:
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
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[APIConsumerUsageResponse | Problem]:
    """Read durable minute usage for one API consumer.

     Returns idempotent request, error, and billable-unit counters for the
    selected stable consumer. The ledger is independent from sampled
    request telemetry. Anonymous traffic is retained separately and is
    not charged to this consumer identity.

    Args:
        slug (str):
        consumer_id (UUID):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageResponse | Problem]
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
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> APIConsumerUsageResponse | Problem | None:
    """Read durable minute usage for one API consumer.

     Returns idempotent request, error, and billable-unit counters for the
    selected stable consumer. The ledger is independent from sampled
    request telemetry. Anonymous traffic is retained separately and is
    not charged to this consumer identity.

    Args:
        slug (str):
        consumer_id (UUID):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageResponse | Problem
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
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> Response[APIConsumerUsageResponse | Problem]:
    """Read durable minute usage for one API consumer.

     Returns idempotent request, error, and billable-unit counters for the
    selected stable consumer. The ledger is independent from sampled
    request telemetry. Anonymous traffic is retained separately and is
    not charged to this consumer identity.

    Args:
        slug (str):
        consumer_id (UUID):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageResponse | Problem]
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
    since: datetime.datetime | Unset = UNSET,
    until: datetime.datetime | Unset = UNSET,
) -> APIConsumerUsageResponse | Problem | None:
    """Read durable minute usage for one API consumer.

     Returns idempotent request, error, and billable-unit counters for the
    selected stable consumer. The ledger is independent from sampled
    request telemetry. Anonymous traffic is retained separately and is
    not charged to this consumer identity.

    Args:
        slug (str):
        consumer_id (UUID):
        since (datetime.datetime | Unset):
        until (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageResponse | Problem
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
