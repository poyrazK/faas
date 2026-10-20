from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_s3_copy_source import ObjectS3CopySource
from ...models.problem import Problem
from ...models.set_object_s3_copy_source_request import SetObjectS3CopySourceRequest
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    credential: UUID,
    source: UUID,
    *,
    body: SetObjectS3CopySourceRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/buckets/{bucket}/s3-credentials/{credential}/copy-sources/{source}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            credential=quote(str(credential), safe=""),
            source=quote(str(source), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectS3CopySource | Problem:
    if response.status_code == 200:
        response_200 = ObjectS3CopySource.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectS3CopySource | Problem]:
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
    body: SetObjectS3CopySourceRequest,
) -> Response[ObjectS3CopySource | Problem]:
    """Grant an owned bucket as a copy source

     Requires storage:manage, destination write and source read authority. Both buckets must be ready on
    the same native S3 placement. The prefix is literal and at most 1024 UTF-8 bytes. Identical retries
    preserve authority; changing or recreating a grant invalidates prepared copies. Signed URL
    credentials and rotation stages cannot acquire grants. CopySource uses the source bucket UUID
    followed by the encoded object key.

    Args:
        slug (str):
        bucket (UUID):
        credential (UUID):
        source (UUID):
        body (SetObjectS3CopySourceRequest): Exact source key prefix. Empty or omitted prefix
            grants all keys in the owned source bucket solely for copy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectS3CopySource | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        credential=credential,
        source=source,
        body=body,
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
    body: SetObjectS3CopySourceRequest,
) -> ObjectS3CopySource | Problem | None:
    """Grant an owned bucket as a copy source

     Requires storage:manage, destination write and source read authority. Both buckets must be ready on
    the same native S3 placement. The prefix is literal and at most 1024 UTF-8 bytes. Identical retries
    preserve authority; changing or recreating a grant invalidates prepared copies. Signed URL
    credentials and rotation stages cannot acquire grants. CopySource uses the source bucket UUID
    followed by the encoded object key.

    Args:
        slug (str):
        bucket (UUID):
        credential (UUID):
        source (UUID):
        body (SetObjectS3CopySourceRequest): Exact source key prefix. Empty or omitted prefix
            grants all keys in the owned source bucket solely for copy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectS3CopySource | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        credential=credential,
        source=source,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    credential: UUID,
    source: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: SetObjectS3CopySourceRequest,
) -> Response[ObjectS3CopySource | Problem]:
    """Grant an owned bucket as a copy source

     Requires storage:manage, destination write and source read authority. Both buckets must be ready on
    the same native S3 placement. The prefix is literal and at most 1024 UTF-8 bytes. Identical retries
    preserve authority; changing or recreating a grant invalidates prepared copies. Signed URL
    credentials and rotation stages cannot acquire grants. CopySource uses the source bucket UUID
    followed by the encoded object key.

    Args:
        slug (str):
        bucket (UUID):
        credential (UUID):
        source (UUID):
        body (SetObjectS3CopySourceRequest): Exact source key prefix. Empty or omitted prefix
            grants all keys in the owned source bucket solely for copy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectS3CopySource | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        credential=credential,
        source=source,
        body=body,
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
    body: SetObjectS3CopySourceRequest,
) -> ObjectS3CopySource | Problem | None:
    """Grant an owned bucket as a copy source

     Requires storage:manage, destination write and source read authority. Both buckets must be ready on
    the same native S3 placement. The prefix is literal and at most 1024 UTF-8 bytes. Identical retries
    preserve authority; changing or recreating a grant invalidates prepared copies. Signed URL
    credentials and rotation stages cannot acquire grants. CopySource uses the source bucket UUID
    followed by the encoded object key.

    Args:
        slug (str):
        bucket (UUID):
        credential (UUID):
        source (UUID):
        body (SetObjectS3CopySourceRequest): Exact source key prefix. Empty or omitted prefix
            grants all keys in the owned source bucket solely for copy.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectS3CopySource | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            credential=credential,
            source=source,
            client=client,
            body=body,
        )
    ).parsed
