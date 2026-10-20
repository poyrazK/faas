from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_access_grant import ObjectBucketAccessGrant
from ...models.problem import Problem
from ...models.set_object_bucket_access_grant_request import SetObjectBucketAccessGrantRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    key: UUID,
    *,
    body: SetObjectBucketAccessGrantRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/buckets/{bucket}/access-grants/{key}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            key=quote(str(key), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectBucketAccessGrant | Problem:
    if response.status_code == 200:
        response_200 = ObjectBucketAccessGrant.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketAccessGrant | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    key: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SetObjectBucketAccessGrantRequest,
) -> Response[ObjectBucketAccessGrant | Problem]:
    """Create or replace an API-key grant for a bucket

     Requires storage:manage or admin. The target key must be active or in grace and carry the storage
    scopes needed by the requested permission. Admin keys do not need and cannot receive grants.

    Args:
        slug (str):
        bucket (UUID):
        key (UUID):
        body (SetObjectBucketAccessGrantRequest): Desired data-plane permission for the target API
            key.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketAccessGrant | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        key=key,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    key: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SetObjectBucketAccessGrantRequest,
) -> ObjectBucketAccessGrant | Problem | None:
    """Create or replace an API-key grant for a bucket

     Requires storage:manage or admin. The target key must be active or in grace and carry the storage
    scopes needed by the requested permission. Admin keys do not need and cannot receive grants.

    Args:
        slug (str):
        bucket (UUID):
        key (UUID):
        body (SetObjectBucketAccessGrantRequest): Desired data-plane permission for the target API
            key.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketAccessGrant | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        key=key,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    key: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SetObjectBucketAccessGrantRequest,
) -> Response[ObjectBucketAccessGrant | Problem]:
    """Create or replace an API-key grant for a bucket

     Requires storage:manage or admin. The target key must be active or in grace and carry the storage
    scopes needed by the requested permission. Admin keys do not need and cannot receive grants.

    Args:
        slug (str):
        bucket (UUID):
        key (UUID):
        body (SetObjectBucketAccessGrantRequest): Desired data-plane permission for the target API
            key.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketAccessGrant | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        key=key,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    key: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SetObjectBucketAccessGrantRequest,
) -> ObjectBucketAccessGrant | Problem | None:
    """Create or replace an API-key grant for a bucket

     Requires storage:manage or admin. The target key must be active or in grace and carry the storage
    scopes needed by the requested permission. Admin keys do not need and cannot receive grants.

    Args:
        slug (str):
        bucket (UUID):
        key (UUID):
        body (SetObjectBucketAccessGrantRequest): Desired data-plane permission for the target API
            key.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketAccessGrant | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            key=key,
            client=client,
            body=body,
        )
    ).parsed
