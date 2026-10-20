from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_deletion import ObjectDeletion
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    deletion: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/objects/deletions/{deletion}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            deletion=quote(str(deletion), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectDeletion | Problem:
    if response.status_code == 200:
        response_200 = ObjectDeletion.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectDeletion | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    deletion: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectDeletion | Problem]:
    """Read a durable deletion receipt

     Requires storage write scope and the bucket write grant. Returns only the bucket-owned receipt and
    public identities. Cache-Control no-store.

    Args:
        slug (str):
        bucket (UUID):
        deletion (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectDeletion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        deletion=deletion,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    deletion: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectDeletion | Problem | None:
    """Read a durable deletion receipt

     Requires storage write scope and the bucket write grant. Returns only the bucket-owned receipt and
    public identities. Cache-Control no-store.

    Args:
        slug (str):
        bucket (UUID):
        deletion (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectDeletion | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        deletion=deletion,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    deletion: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectDeletion | Problem]:
    """Read a durable deletion receipt

     Requires storage write scope and the bucket write grant. Returns only the bucket-owned receipt and
    public identities. Cache-Control no-store.

    Args:
        slug (str):
        bucket (UUID):
        deletion (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectDeletion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        deletion=deletion,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    deletion: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectDeletion | Problem | None:
    """Read a durable deletion receipt

     Requires storage write scope and the bucket write grant. Returns only the bucket-owned receipt and
    public identities. Cache-Control no-store.

    Args:
        slug (str):
        bucket (UUID):
        deletion (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectDeletion | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            deletion=deletion,
            client=client,
        )
    ).parsed
