from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_lifecycle import ObjectBucketLifecycle
from ...models.object_bucket_lifecycle_request import ObjectBucketLifecycleRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: ObjectBucketLifecycleRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/buckets/{bucket}/lifecycle".format(
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
) -> ObjectBucketLifecycle | Problem:
    if response.status_code == 200:
        response_200 = ObjectBucketLifecycle.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketLifecycle | Problem]:
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
    body: ObjectBucketLifecycleRequest,
) -> Response[ObjectBucketLifecycle | Problem]:
    """Replace durable bucket lifecycle rules

     Requires storage manage scope and the bucket write grant. Replaces the entire policy with one to one
    thousand validated rules on a capable backend. Expiration, noncurrent expiration and abandoned
    multipart cleanup use Gregale journals and verified accounting. Transitions and object size
    predicates are unsupported. A live discovery lease returns conflict. New storage ingress must be
    enabled.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketLifecycleRequest): Complete lifecycle rule replacement; use DELETE to
            clear the configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketLifecycle | Problem]
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
    body: ObjectBucketLifecycleRequest,
) -> ObjectBucketLifecycle | Problem | None:
    """Replace durable bucket lifecycle rules

     Requires storage manage scope and the bucket write grant. Replaces the entire policy with one to one
    thousand validated rules on a capable backend. Expiration, noncurrent expiration and abandoned
    multipart cleanup use Gregale journals and verified accounting. Transitions and object size
    predicates are unsupported. A live discovery lease returns conflict. New storage ingress must be
    enabled.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketLifecycleRequest): Complete lifecycle rule replacement; use DELETE to
            clear the configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketLifecycle | Problem
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
    body: ObjectBucketLifecycleRequest,
) -> Response[ObjectBucketLifecycle | Problem]:
    """Replace durable bucket lifecycle rules

     Requires storage manage scope and the bucket write grant. Replaces the entire policy with one to one
    thousand validated rules on a capable backend. Expiration, noncurrent expiration and abandoned
    multipart cleanup use Gregale journals and verified accounting. Transitions and object size
    predicates are unsupported. A live discovery lease returns conflict. New storage ingress must be
    enabled.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketLifecycleRequest): Complete lifecycle rule replacement; use DELETE to
            clear the configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketLifecycle | Problem]
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
    body: ObjectBucketLifecycleRequest,
) -> ObjectBucketLifecycle | Problem | None:
    """Replace durable bucket lifecycle rules

     Requires storage manage scope and the bucket write grant. Replaces the entire policy with one to one
    thousand validated rules on a capable backend. Expiration, noncurrent expiration and abandoned
    multipart cleanup use Gregale journals and verified accounting. Transitions and object size
    predicates are unsupported. A live discovery lease returns conflict. New storage ingress must be
    enabled.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketLifecycleRequest): Complete lifecycle rule replacement; use DELETE to
            clear the configuration.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketLifecycle | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
        )
    ).parsed
