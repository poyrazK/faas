from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.create_object_s3_credential_request import CreateObjectS3CredentialRequest
from ...models.object_s3_credential_secret import ObjectS3CredentialSecret
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: CreateObjectS3CredentialRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/buckets/{bucket}/s3-credentials".format(
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
) -> ObjectS3CredentialSecret | Problem:
    if response.status_code == 201:
        response_201 = ObjectS3CredentialSecret.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectS3CredentialSecret | Problem]:
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
    body: CreateObjectS3CredentialRequest,
) -> Response[ObjectS3CredentialSecret | Problem]:
    """Create a bucket-scoped credential for s3.gregale.dev

     The secret access key is returned exactly once, sealed at rest, and never recoverable through the
    control-plane API. At most ten active credentials may exist per bucket.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectS3CredentialRequest): Label and least-privilege access level for a new
            bucket-scoped S3 credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectS3CredentialSecret | Problem]
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
    body: CreateObjectS3CredentialRequest,
) -> ObjectS3CredentialSecret | Problem | None:
    """Create a bucket-scoped credential for s3.gregale.dev

     The secret access key is returned exactly once, sealed at rest, and never recoverable through the
    control-plane API. At most ten active credentials may exist per bucket.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectS3CredentialRequest): Label and least-privilege access level for a new
            bucket-scoped S3 credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectS3CredentialSecret | Problem
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
    body: CreateObjectS3CredentialRequest,
) -> Response[ObjectS3CredentialSecret | Problem]:
    """Create a bucket-scoped credential for s3.gregale.dev

     The secret access key is returned exactly once, sealed at rest, and never recoverable through the
    control-plane API. At most ten active credentials may exist per bucket.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectS3CredentialRequest): Label and least-privilege access level for a new
            bucket-scoped S3 credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectS3CredentialSecret | Problem]
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
    body: CreateObjectS3CredentialRequest,
) -> ObjectS3CredentialSecret | Problem | None:
    """Create a bucket-scoped credential for s3.gregale.dev

     The secret access key is returned exactly once, sealed at rest, and never recoverable through the
    control-plane API. At most ten active credentials may exist per bucket.

    Args:
        slug (str):
        bucket (UUID):
        body (CreateObjectS3CredentialRequest): Label and least-privilege access level for a new
            bucket-scoped S3 credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectS3CredentialSecret | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
        )
    ).parsed
