from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.create_object_multipart_upload_request import CreateObjectMultipartUploadRequest
from ...models.object_multipart_upload import ObjectMultipartUpload
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: CreateObjectMultipartUploadRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/buckets/{bucket}/multipart-uploads".format(
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
) -> ObjectMultipartUpload | Problem:
    if response.status_code == 200:
        response_200 = ObjectMultipartUpload.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = ObjectMultipartUpload.from_dict(response.json())

        return response_201

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
    *,
    client: AuthenticatedClient | Client,
    body: CreateObjectMultipartUploadRequest,
) -> Response[ObjectMultipartUpload | Problem]:
    """Start or recover a resumable multipart upload

     Requires storage:write or admin and a matching bucket grant. Gregale
    reserves the complete declared size before creating billable provider
    parts. A retry with the same live bucket/key, size, and content type
    returns the existing session; conflicting parameters return 409. The
    provider upload ID remains private. Sessions expire after 24 hours.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectMultipartUploadRequest): Final object identity and total size for a
            resumable upload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectMultipartUpload | Problem]
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
    body: CreateObjectMultipartUploadRequest,
) -> ObjectMultipartUpload | Problem | None:
    """Start or recover a resumable multipart upload

     Requires storage:write or admin and a matching bucket grant. Gregale
    reserves the complete declared size before creating billable provider
    parts. A retry with the same live bucket/key, size, and content type
    returns the existing session; conflicting parameters return 409. The
    provider upload ID remains private. Sessions expire after 24 hours.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectMultipartUploadRequest): Final object identity and total size for a
            resumable upload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectMultipartUpload | Problem
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
    body: CreateObjectMultipartUploadRequest,
) -> Response[ObjectMultipartUpload | Problem]:
    """Start or recover a resumable multipart upload

     Requires storage:write or admin and a matching bucket grant. Gregale
    reserves the complete declared size before creating billable provider
    parts. A retry with the same live bucket/key, size, and content type
    returns the existing session; conflicting parameters return 409. The
    provider upload ID remains private. Sessions expire after 24 hours.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectMultipartUploadRequest): Final object identity and total size for a
            resumable upload.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectMultipartUpload | Problem]
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
    body: CreateObjectMultipartUploadRequest,
) -> ObjectMultipartUpload | Problem | None:
    """Start or recover a resumable multipart upload

     Requires storage:write or admin and a matching bucket grant. Gregale
    reserves the complete declared size before creating billable provider
    parts. A retry with the same live bucket/key, size, and content type
    returns the existing session; conflicting parameters return 409. The
    provider upload ID remains private. Sessions expire after 24 hours.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectMultipartUploadRequest): Final object identity and total size for a
            resumable upload.

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
            client=client,
            body=body,
        )
    ).parsed
