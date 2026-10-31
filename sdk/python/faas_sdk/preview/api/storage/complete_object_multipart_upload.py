from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.complete_object_multipart_upload_request import CompleteObjectMultipartUploadRequest
from ...models.object_multipart_upload import ObjectMultipartUpload
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    body: CompleteObjectMultipartUploadRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/complete".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            upload=quote(str(upload), safe=""),
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
    body: CompleteObjectMultipartUploadRequest,
) -> Response[ObjectMultipartUpload | Problem]:
    """Assemble all uploaded parts into the final object

     Requires storage:write or admin and a matching bucket grant. Supply one
    ETag for every part in ascending order. Completion intent is persisted
    before contacting the provider and recovered after crashes. An identical
    retry after completion returns the completed session without repeating
    the provider operation.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        body (CompleteObjectMultipartUploadRequest): Complete ordered ETag manifest for every part
            in the session.

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
        body=body,
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
    body: CompleteObjectMultipartUploadRequest,
) -> ObjectMultipartUpload | Problem | None:
    """Assemble all uploaded parts into the final object

     Requires storage:write or admin and a matching bucket grant. Supply one
    ETag for every part in ascending order. Completion intent is persisted
    before contacting the provider and recovered after crashes. An identical
    retry after completion returns the completed session without repeating
    the provider operation.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        body (CompleteObjectMultipartUploadRequest): Complete ordered ETag manifest for every part
            in the session.

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
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CompleteObjectMultipartUploadRequest,
) -> Response[ObjectMultipartUpload | Problem]:
    """Assemble all uploaded parts into the final object

     Requires storage:write or admin and a matching bucket grant. Supply one
    ETag for every part in ascending order. Completion intent is persisted
    before contacting the provider and recovered after crashes. An identical
    retry after completion returns the completed session without repeating
    the provider operation.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        body (CompleteObjectMultipartUploadRequest): Complete ordered ETag manifest for every part
            in the session.

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
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: CompleteObjectMultipartUploadRequest,
) -> ObjectMultipartUpload | Problem | None:
    """Assemble all uploaded parts into the final object

     Requires storage:write or admin and a matching bucket grant. Supply one
    ETag for every part in ascending order. Completion intent is persisted
    before contacting the provider and recovered after crashes. An identical
    retry after completion returns the completed session without repeating
    the provider operation.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        body (CompleteObjectMultipartUploadRequest): Complete ordered ETag manifest for every part
            in the session.

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
            body=body,
        )
    ).parsed
