from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_consumer_usage_alert_list_response import APIConsumerUsageAlertListResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    consumer_id: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/consumers/{consumer_id}/usage-alerts".format(
            slug=quote(str(slug), safe=""),
            consumer_id=quote(str(consumer_id), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APIConsumerUsageAlertListResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APIConsumerUsageAlertListResponse.from_dict(response.json())

        return response_200

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
) -> Response[APIConsumerUsageAlertListResponse | Problem]:
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
) -> Response[APIConsumerUsageAlertListResponse | Problem]:
    """List the plan usage alerts a consumer crossed.

     Returns up to 100 recorded crossings of the consumer's plan alert
    thresholds, newest first. Each crossing also emitted one
    consumer.usage_threshold webhook; this list reconciles missed
    deliveries.

    Args:
        slug (str):
        consumer_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageAlertListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
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
) -> APIConsumerUsageAlertListResponse | Problem | None:
    """List the plan usage alerts a consumer crossed.

     Returns up to 100 recorded crossings of the consumer's plan alert
    thresholds, newest first. Each crossing also emitted one
    consumer.usage_threshold webhook; this list reconciles missed
    deliveries.

    Args:
        slug (str):
        consumer_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageAlertListResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        consumer_id=consumer_id,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[APIConsumerUsageAlertListResponse | Problem]:
    """List the plan usage alerts a consumer crossed.

     Returns up to 100 recorded crossings of the consumer's plan alert
    thresholds, newest first. Each crossing also emitted one
    consumer.usage_threshold webhook; this list reconciles missed
    deliveries.

    Args:
        slug (str):
        consumer_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APIConsumerUsageAlertListResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        consumer_id=consumer_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    consumer_id: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> APIConsumerUsageAlertListResponse | Problem | None:
    """List the plan usage alerts a consumer crossed.

     Returns up to 100 recorded crossings of the consumer's plan alert
    thresholds, newest first. Each crossing also emitted one
    consumer.usage_threshold webhook; this list reconciles missed
    deliveries.

    Args:
        slug (str):
        consumer_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APIConsumerUsageAlertListResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            consumer_id=consumer_id,
            client=client,
        )
    ).parsed
