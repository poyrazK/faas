from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_multipart_upload import ObjectMultipartUpload
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    upload: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            upload=quote(str(upload), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectMultipartUpload | Problem:
    if response.status_code == 200:
        response_200 = ObjectMultipartUpload.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectMultipartUpload | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectMultipartUpload | Problem]:
    """Read resumable upload state and part layout

     Requires storage:write or admin and a matching bucket grant. Provider credentials and upload IDs are
    never returned.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectMultipartUpload | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        upload=upload,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectMultipartUpload | Problem | None:
    """Read resumable upload state and part layout

     Requires storage:write or admin and a matching bucket grant. Provider credentials and upload IDs are
    never returned.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectMultipartUpload | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        upload=upload,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectMultipartUpload | Problem]:
    """Read resumable upload state and part layout

     Requires storage:write or admin and a matching bucket grant. Provider credentials and upload IDs are
    never returned.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectMultipartUpload | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        upload=upload,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectMultipartUpload | Problem | None:
    """Read resumable upload state and part layout

     Requires storage:write or admin and a matching bucket grant. Provider credentials and upload IDs are
    never returned.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectMultipartUpload | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            upload=upload,
            client=client,
        )
    ).parsed
