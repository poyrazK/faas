from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_bucket_encryption import ObjectBucketEncryption
from ...models.object_bucket_encryption_request import ObjectBucketEncryptionRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: ObjectBucketEncryptionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/apps/{slug}/buckets/{bucket}/encryption".format(
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
) -> ObjectBucketEncryption | Problem:
    if response.status_code == 202:
        response_202 = ObjectBucketEncryption.from_dict(response.json())

        return response_202

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucketEncryption | Problem]:
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
    body: ObjectBucketEncryptionRequest,
) -> Response[ObjectBucketEncryption | Problem]:
    """Request a verified bucket default encryption policy

     Requires storage manage scope and the bucket write grant. Supports enrolled S3 AES256 and owned KMS
    or DSSE keys, with supported bucket key settings. Per-object contexts are rejected. Each new
    implicit write captures the verified default atomically; explicit write and route selections
    override it. Accepted receipts, URLs and multipart sessions retain their snapshots through changes
    and restart. New implicit admissions wait while configuration is pending. Exact native readback is
    required; unrelated native blocking settings are preserved.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketEncryptionRequest): A non-empty enrolled default policy without per-
            object context.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketEncryption | Problem]
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
    body: ObjectBucketEncryptionRequest,
) -> ObjectBucketEncryption | Problem | None:
    """Request a verified bucket default encryption policy

     Requires storage manage scope and the bucket write grant. Supports enrolled S3 AES256 and owned KMS
    or DSSE keys, with supported bucket key settings. Per-object contexts are rejected. Each new
    implicit write captures the verified default atomically; explicit write and route selections
    override it. Accepted receipts, URLs and multipart sessions retain their snapshots through changes
    and restart. New implicit admissions wait while configuration is pending. Exact native readback is
    required; unrelated native blocking settings are preserved.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketEncryptionRequest): A non-empty enrolled default policy without per-
            object context.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketEncryption | Problem
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
    body: ObjectBucketEncryptionRequest,
) -> Response[ObjectBucketEncryption | Problem]:
    """Request a verified bucket default encryption policy

     Requires storage manage scope and the bucket write grant. Supports enrolled S3 AES256 and owned KMS
    or DSSE keys, with supported bucket key settings. Per-object contexts are rejected. Each new
    implicit write captures the verified default atomically; explicit write and route selections
    override it. Accepted receipts, URLs and multipart sessions retain their snapshots through changes
    and restart. New implicit admissions wait while configuration is pending. Exact native readback is
    required; unrelated native blocking settings are preserved.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketEncryptionRequest): A non-empty enrolled default policy without per-
            object context.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucketEncryption | Problem]
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
    body: ObjectBucketEncryptionRequest,
) -> ObjectBucketEncryption | Problem | None:
    """Request a verified bucket default encryption policy

     Requires storage manage scope and the bucket write grant. Supports enrolled S3 AES256 and owned KMS
    or DSSE keys, with supported bucket key settings. Per-object contexts are rejected. Each new
    implicit write captures the verified default atomically; explicit write and route selections
    override it. Accepted receipts, URLs and multipart sessions retain their snapshots through changes
    and restart. New implicit admissions wait while configuration is pending. Exact native readback is
    required; unrelated native blocking settings are preserved.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectBucketEncryptionRequest): A non-empty enrolled default policy without per-
            object context.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucketEncryption | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
        )
    ).parsed
