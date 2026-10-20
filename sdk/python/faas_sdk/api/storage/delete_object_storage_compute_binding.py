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
    binding: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/buckets/{bucket}/compute-bindings/{binding}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            binding=quote(str(binding), safe=""),
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
    binding: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke a compute binding

     Revokes the bucket-scoped S3 credential before removing its sealed app secrets. The operation is
    idempotent after revocation.

    Args:
        slug (str):
        bucket (UUID):
        binding (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        binding=binding,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    binding: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke a compute binding

     Revokes the bucket-scoped S3 credential before removing its sealed app secrets. The operation is
    idempotent after revocation.

    Args:
        slug (str):
        bucket (UUID):
        binding (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        binding=binding,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    binding: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | Problem]:
    """Revoke a compute binding

     Revokes the bucket-scoped S3 credential before removing its sealed app secrets. The operation is
    idempotent after revocation.

    Args:
        slug (str):
        bucket (UUID):
        binding (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        binding=binding,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    binding: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Any | Problem | None:
    """Revoke a compute binding

     Revokes the bucket-scoped S3 credential before removing its sealed app secrets. The operation is
    idempotent after revocation.

    Args:
        slug (str):
        bucket (UUID):
        binding (UUID):

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
            binding=binding,
            client=client,
        )
    ).parsed
