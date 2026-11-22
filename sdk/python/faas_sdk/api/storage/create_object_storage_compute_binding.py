from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.create_object_storage_compute_binding_request import CreateObjectStorageComputeBindingRequest
from ...models.object_storage_compute_binding import ObjectStorageComputeBinding
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: CreateObjectStorageComputeBindingRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/buckets/{bucket}/compute-bindings".format(
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
) -> ObjectStorageComputeBinding | Problem:
    if response.status_code == 201:
        response_201 = ObjectStorageComputeBinding.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectStorageComputeBinding | Problem]:
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
    body: CreateObjectStorageComputeBindingRequest,
) -> Response[ObjectStorageComputeBinding | Problem]:
    """Bind a bucket to an app's compute environment

     Creates a bucket-scoped Gregale S3 credential and injects endpoint, region, bucket, access-key,
    secret-key, and addressing settings as sealed app secrets. The secret values are never returned.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectStorageComputeBindingRequest): Least-privilege S3 access and the
            environment-variable prefix for an app-to-bucket binding.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectStorageComputeBinding | Problem]
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
    body: CreateObjectStorageComputeBindingRequest,
) -> ObjectStorageComputeBinding | Problem | None:
    """Bind a bucket to an app's compute environment

     Creates a bucket-scoped Gregale S3 credential and injects endpoint, region, bucket, access-key,
    secret-key, and addressing settings as sealed app secrets. The secret values are never returned.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectStorageComputeBindingRequest): Least-privilege S3 access and the
            environment-variable prefix for an app-to-bucket binding.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectStorageComputeBinding | Problem
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
    body: CreateObjectStorageComputeBindingRequest,
) -> Response[ObjectStorageComputeBinding | Problem]:
    """Bind a bucket to an app's compute environment

     Creates a bucket-scoped Gregale S3 credential and injects endpoint, region, bucket, access-key,
    secret-key, and addressing settings as sealed app secrets. The secret values are never returned.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectStorageComputeBindingRequest): Least-privilege S3 access and the
            environment-variable prefix for an app-to-bucket binding.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectStorageComputeBinding | Problem]
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
    body: CreateObjectStorageComputeBindingRequest,
) -> ObjectStorageComputeBinding | Problem | None:
    """Bind a bucket to an app's compute environment

     Creates a bucket-scoped Gregale S3 credential and injects endpoint, region, bucket, access-key,
    secret-key, and addressing settings as sealed app secrets. The secret values are never returned.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectStorageComputeBindingRequest): Least-privilege S3 access and the
            environment-variable prefix for an app-to-bucket binding.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectStorageComputeBinding | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
        )
    ).parsed
