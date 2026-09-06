from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    key: str,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["key"] = key

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "delete",
        "url": "/v1/apps/{slug}/buckets/{bucket}/objects".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
        "params": params,
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
    *,
    client: AuthenticatedClient | Client,
    key: str,
) -> Response[Any | Problem]:
    """Delete one object by exact key

     Requires storage:write or admin. Non-admin keys also require a write or read_write grant on this
    bucket. With provider-side versioning this may create a delete marker; version management is not
    part of this preview.

    Args:
        slug (str):
        bucket (UUID):
        key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        key=key,
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
    key: str,
) -> Any | Problem | None:
    """Delete one object by exact key

     Requires storage:write or admin. Non-admin keys also require a write or read_write grant on this
    bucket. With provider-side versioning this may create a delete marker; version management is not
    part of this preview.

    Args:
        slug (str):
        bucket (UUID):
        key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
        key=key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    key: str,
) -> Response[Any | Problem]:
    """Delete one object by exact key

     Requires storage:write or admin. Non-admin keys also require a write or read_write grant on this
    bucket. With provider-side versioning this may create a delete marker; version management is not
    part of this preview.

    Args:
        slug (str):
        bucket (UUID):
        key (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        key=key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    key: str,
) -> Any | Problem | None:
    """Delete one object by exact key

     Requires storage:write or admin. Non-admin keys also require a write or read_write grant on this
    bucket. With provider-side versioning this may create a delete marker; version management is not
    part of this preview.

    Args:
        slug (str):
        bucket (UUID):
        key (str):

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
            client=client,
            key=key,
        )
    ).parsed
