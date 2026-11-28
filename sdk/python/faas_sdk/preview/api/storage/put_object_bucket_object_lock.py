from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_object_lock import ObjectBucketObjectLock
from ...models.object_bucket_object_lock_request import ObjectBucketObjectLockRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: ObjectBucketObjectLockRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/buckets/{bucket}/object-lock".format(
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
) -> ObjectBucketObjectLock | Problem:
    if response.status_code == 202:
        response_202 = ObjectBucketObjectLock.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketObjectLock | Problem]:
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
    body: ObjectBucketObjectLockRequest,
) -> Response[ObjectBucketObjectLock | Problem]:
    """Request permanent Object Lock enablement or a default retention change

     Requires storage manage scope, MFA where required and the bucket write grant. Enabled must be true.
    Omit default_retention to clear defaults for future versions while retaining permanent enablement
    and existing protection. GOVERNANCE and COMPLIANCE support a fixed duration, an enrolled event hold
    duration or both. New writes remain fenced until accepted transfers drain, versioning is Enabled,
    propagation and a fresh all-version inventory finish, and native configuration is verified.
    Identical pending requests are idempotent; opposite pending requests conflict. Accepted recovery
    continues after ingress or capability enrollment is disabled. Per-version lock management is a
    separate capability.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketObjectLockRequest): An enabled-only permanent bucket configuration
            request; omitted defaults clear future defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketObjectLock | Problem]
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
    body: ObjectBucketObjectLockRequest,
) -> ObjectBucketObjectLock | Problem | None:
    """Request permanent Object Lock enablement or a default retention change

     Requires storage manage scope, MFA where required and the bucket write grant. Enabled must be true.
    Omit default_retention to clear defaults for future versions while retaining permanent enablement
    and existing protection. GOVERNANCE and COMPLIANCE support a fixed duration, an enrolled event hold
    duration or both. New writes remain fenced until accepted transfers drain, versioning is Enabled,
    propagation and a fresh all-version inventory finish, and native configuration is verified.
    Identical pending requests are idempotent; opposite pending requests conflict. Accepted recovery
    continues after ingress or capability enrollment is disabled. Per-version lock management is a
    separate capability.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketObjectLockRequest): An enabled-only permanent bucket configuration
            request; omitted defaults clear future defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketObjectLock | Problem
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
    body: ObjectBucketObjectLockRequest,
) -> Response[ObjectBucketObjectLock | Problem]:
    """Request permanent Object Lock enablement or a default retention change

     Requires storage manage scope, MFA where required and the bucket write grant. Enabled must be true.
    Omit default_retention to clear defaults for future versions while retaining permanent enablement
    and existing protection. GOVERNANCE and COMPLIANCE support a fixed duration, an enrolled event hold
    duration or both. New writes remain fenced until accepted transfers drain, versioning is Enabled,
    propagation and a fresh all-version inventory finish, and native configuration is verified.
    Identical pending requests are idempotent; opposite pending requests conflict. Accepted recovery
    continues after ingress or capability enrollment is disabled. Per-version lock management is a
    separate capability.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketObjectLockRequest): An enabled-only permanent bucket configuration
            request; omitted defaults clear future defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketObjectLock | Problem]
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
    body: ObjectBucketObjectLockRequest,
) -> ObjectBucketObjectLock | Problem | None:
    """Request permanent Object Lock enablement or a default retention change

     Requires storage manage scope, MFA where required and the bucket write grant. Enabled must be true.
    Omit default_retention to clear defaults for future versions while retaining permanent enablement
    and existing protection. GOVERNANCE and COMPLIANCE support a fixed duration, an enrolled event hold
    duration or both. New writes remain fenced until accepted transfers drain, versioning is Enabled,
    propagation and a fresh all-version inventory finish, and native configuration is verified.
    Identical pending requests are idempotent; opposite pending requests conflict. Accepted recovery
    continues after ingress or capability enrollment is disabled. Per-version lock management is a
    separate capability.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketObjectLockRequest): An enabled-only permanent bucket configuration
            request; omitted defaults clear future defaults.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketObjectLock | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
        )
    ).parsed
