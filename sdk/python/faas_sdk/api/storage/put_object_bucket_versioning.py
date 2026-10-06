from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_versioning import ObjectBucketVersioning
from ...models.object_bucket_versioning_request import ObjectBucketVersioningRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: ObjectBucketVersioningRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/buckets/{bucket}/versioning".format(
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
) -> ObjectBucketVersioning | Problem:
    if response.status_code == 202:
        response_202 = ObjectBucketVersioning.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketVersioning | Problem]:
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
    body: ObjectBucketVersioningRequest,
) -> Response[ObjectBucketVersioning | Problem]:
    """Request durable bucket versioning configuration

     Requires storage manage scope and the bucket write grant. Enabled and Suspended are supported on
    capable S3 backends. Opposite targets conflict until the current transition is ready. New writes and
    bucket deletion remain fenced while existing work drains, configuration propagates for at least
    fifteen minutes and a complete all-version inventory commits. Unresolved legacy write grants reject
    the request. Cancellation cannot reopen writes. Suspension retains all-version accounting. MFA
    Delete changes are unsupported.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketVersioningRequest): Desired Enabled or Suspended status for a durable
            bucket configuration cutover.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketVersioning | Problem]
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
    body: ObjectBucketVersioningRequest,
) -> ObjectBucketVersioning | Problem | None:
    """Request durable bucket versioning configuration

     Requires storage manage scope and the bucket write grant. Enabled and Suspended are supported on
    capable S3 backends. Opposite targets conflict until the current transition is ready. New writes and
    bucket deletion remain fenced while existing work drains, configuration propagates for at least
    fifteen minutes and a complete all-version inventory commits. Unresolved legacy write grants reject
    the request. Cancellation cannot reopen writes. Suspension retains all-version accounting. MFA
    Delete changes are unsupported.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketVersioningRequest): Desired Enabled or Suspended status for a durable
            bucket configuration cutover.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketVersioning | Problem
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
    body: ObjectBucketVersioningRequest,
) -> Response[ObjectBucketVersioning | Problem]:
    """Request durable bucket versioning configuration

     Requires storage manage scope and the bucket write grant. Enabled and Suspended are supported on
    capable S3 backends. Opposite targets conflict until the current transition is ready. New writes and
    bucket deletion remain fenced while existing work drains, configuration propagates for at least
    fifteen minutes and a complete all-version inventory commits. Unresolved legacy write grants reject
    the request. Cancellation cannot reopen writes. Suspension retains all-version accounting. MFA
    Delete changes are unsupported.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketVersioningRequest): Desired Enabled or Suspended status for a durable
            bucket configuration cutover.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketVersioning | Problem]
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
    body: ObjectBucketVersioningRequest,
) -> ObjectBucketVersioning | Problem | None:
    """Request durable bucket versioning configuration

     Requires storage manage scope and the bucket write grant. Enabled and Suspended are supported on
    capable S3 backends. Opposite targets conflict until the current transition is ready. New writes and
    bucket deletion remain fenced while existing work drains, configuration propagates for at least
    fifteen minutes and a complete all-version inventory commits. Unresolved legacy write grants reject
    the request. Cancellation cannot reopen writes. Suspension retains all-version accounting. MFA
    Delete changes are unsupported.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketVersioningRequest): Desired Enabled or Suspended status for a durable
            bucket configuration cutover.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketVersioning | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
        )
    ).parsed
