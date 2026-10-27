from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    credential: UUID,
    source: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}/copy-sources/{source}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            credential=quote(str(credential), safe=""),
            source=quote(str(source), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    credential: UUID,
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke a copy-only source grant

     Requires storage:manage and destination write authority. Prevents new copy dispatch while preserving
    already dispatched completion and recovery. Cleanup remains available for revoked destination
    credentials and disabled S3 ingress.

    Args:
        slug (str):
        bucket (UUID):
        credential (UUID):
        source (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        credential=credential,
        source=source,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    credential: UUID,
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke a copy-only source grant

     Requires storage:manage and destination write authority. Prevents new copy dispatch while preserving
    already dispatched completion and recovery. Cleanup remains available for revoked destination
    credentials and disabled S3 ingress.

    Args:
        slug (str):
        bucket (UUID):
        credential (UUID):
        source (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        credential=credential,
        source=source,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    credential: UUID,
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke a copy-only source grant

     Requires storage:manage and destination write authority. Prevents new copy dispatch while preserving
    already dispatched completion and recovery. Cleanup remains available for revoked destination
    credentials and disabled S3 ingress.

    Args:
        slug (str):
        bucket (UUID):
        credential (UUID):
        source (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        credential=credential,
        source=source,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    credential: UUID,
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke a copy-only source grant

     Requires storage:manage and destination write authority. Prevents new copy dispatch while preserving
    already dispatched completion and recovery. Cleanup remains available for revoked destination
    credentials and disabled S3 ingress.

    Args:
        slug (str):
        bucket (UUID):
        credential (UUID):
        source (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            credential=credential,
            source=source,
            client=client,
        )
    ).parsed
