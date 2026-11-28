from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_encryption_capabilities import ObjectEncryptionCapabilities
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/encryption-capabilities".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectEncryptionCapabilities | Problem:
    if response.status_code == 200:
        response_200 = ObjectEncryptionCapabilities.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectEncryptionCapabilities | Problem]:
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
) -> Response[ObjectEncryptionCapabilities | Problem]:
    """List enrolled encryption algorithms and owned key references

     Requires storage write scope and the bucket write grant. Returns only the bucket owner's enrolled
    Gregale key references for explicit S3 PUT, copy and multipart initialization. Discovery makes no
    provider requests and does not establish key health or effective native permissions. Cache-Control
    no-store. Available while new storage ingress is disabled.

    Args:
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectEncryptionCapabilities | Problem]
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
) -> ObjectEncryptionCapabilities | Problem | None:
    """List enrolled encryption algorithms and owned key references

     Requires storage write scope and the bucket write grant. Returns only the bucket owner's enrolled
    Gregale key references for explicit S3 PUT, copy and multipart initialization. Discovery makes no
    provider requests and does not establish key health or effective native permissions. Cache-Control
    no-store. Available while new storage ingress is disabled.

    Args:
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectEncryptionCapabilities | Problem
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
) -> Response[ObjectEncryptionCapabilities | Problem]:
    """List enrolled encryption algorithms and owned key references

     Requires storage write scope and the bucket write grant. Returns only the bucket owner's enrolled
    Gregale key references for explicit S3 PUT, copy and multipart initialization. Discovery makes no
    provider requests and does not establish key health or effective native permissions. Cache-Control
    no-store. Available while new storage ingress is disabled.

    Args:
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectEncryptionCapabilities | Problem]
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
) -> ObjectEncryptionCapabilities | Problem | None:
    """List enrolled encryption algorithms and owned key references

     Requires storage write scope and the bucket write grant. Returns only the bucket owner's enrolled
    Gregale key references for explicit S3 PUT, copy and multipart initialization. Discovery makes no
    provider requests and does not establish key health or effective native permissions. Cache-Control
    no-store. Available while new storage ingress is disabled.

    Args:
        slug (str):
        bucket (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectEncryptionCapabilities | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
        )
    ).parsed
