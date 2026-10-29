from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_cors import ObjectBucketCORS
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/cors".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectBucketCORS | Problem:
    if response.status_code == 200:
        response_200 = ObjectBucketCORS.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketCORS | Problem]:
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
) -> Response[ObjectBucketCORS | Problem]:
    """Read a bucket's CORS rules

     Requires storage manage scope and the bucket write grant. Returns an empty rule list at revision 0
    when none are set. The branded S3 endpoint applies the rules to browser requests that carry the
    bucket's presigned URLs or upload grants.

    Args:
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketCORS | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
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
) -> ObjectBucketCORS | Problem | None:
    """Read a bucket's CORS rules

     Requires storage manage scope and the bucket write grant. Returns an empty rule list at revision 0
    when none are set. The branded S3 endpoint applies the rules to browser requests that carry the
    bucket's presigned URLs or upload grants.

    Args:
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketCORS | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectBucketCORS | Problem]:
    """Read a bucket's CORS rules

     Requires storage manage scope and the bucket write grant. Returns an empty rule list at revision 0
    when none are set. The branded S3 endpoint applies the rules to browser requests that carry the
    bucket's presigned URLs or upload grants.

    Args:
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketCORS | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectBucketCORS | Problem | None:
    """Read a bucket's CORS rules

     Requires storage manage scope and the bucket write grant. Returns an empty rule list at revision 0
    when none are set. The branded S3 endpoint applies the rules to browser requests that carry the
    bucket's presigned URLs or upload grants.

    Args:
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketCORS | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
        )
    ).parsed
