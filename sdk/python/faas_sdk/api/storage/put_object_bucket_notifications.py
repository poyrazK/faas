from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_notifications import ObjectBucketNotifications
from ...models.object_bucket_notifications_request import (
    ObjectBucketNotificationsRequest,
)
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: ObjectBucketNotificationsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/buckets/{bucket}/notifications".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectBucketNotifications | Problem:
    if response.status_code == 200:
        response_200 = ObjectBucketNotifications.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketNotifications | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectBucketNotificationsRequest,
) -> Response[ObjectBucketNotifications | Problem]:
    """Replace durable bucket notification rules

     Requires storage manage scope and the bucket write grant. Atomically replaces notification rules and
    validates Gregale-owned function or queue ARNs in this account and region. Empty rules clear intent.
    Accepted events retain captured destinations. Nonempty configuration requires storage ingress to be
    enabled.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketNotificationsRequest): Complete atomic replacement; an empty array
            clears notification intent.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketNotifications | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectBucketNotificationsRequest,
) -> ObjectBucketNotifications | Problem | None:
    """Replace durable bucket notification rules

     Requires storage manage scope and the bucket write grant. Atomically replaces notification rules and
    validates Gregale-owned function or queue ARNs in this account and region. Empty rules clear intent.
    Accepted events retain captured destinations. Nonempty configuration requires storage ingress to be
    enabled.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketNotificationsRequest): Complete atomic replacement; an empty array
            clears notification intent.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketNotifications | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectBucketNotificationsRequest,
) -> Response[ObjectBucketNotifications | Problem]:
    """Replace durable bucket notification rules

     Requires storage manage scope and the bucket write grant. Atomically replaces notification rules and
    validates Gregale-owned function or queue ARNs in this account and region. Empty rules clear intent.
    Accepted events retain captured destinations. Nonempty configuration requires storage ingress to be
    enabled.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketNotificationsRequest): Complete atomic replacement; an empty array
            clears notification intent.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketNotifications | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectBucketNotificationsRequest,
) -> ObjectBucketNotifications | Problem | None:
    """Replace durable bucket notification rules

     Requires storage manage scope and the bucket write grant. Atomically replaces notification rules and
    validates Gregale-owned function or queue ARNs in this account and region. Empty rules clear intent.
    Accepted events retain captured destinations. Nonempty configuration requires storage ingress to be
    enabled.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketNotificationsRequest): Complete atomic replacement; an empty array
            clears notification intent.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketNotifications | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
        )
    ).parsed
