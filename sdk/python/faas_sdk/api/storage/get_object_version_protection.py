from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_version_protection import ObjectVersionProtection
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    operation: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/protection-operations/{operation}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            operation=quote(str(operation), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectVersionProtection | Problem:
    if response.status_code == 200:
        response_200 = ObjectVersionProtection.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectVersionProtection | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    operation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectVersionProtection | Problem]:
    """Inspect an owned version protection operation

     Requires storage manage scope and a bucket read grant. Available with ingress or enrollment
    disabled. Provider identities and worker leases are private.

    Args:
        slug (str):
        bucket (UUID):
        operation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectVersionProtection | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        operation=operation,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    operation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectVersionProtection | Problem | None:
    """Inspect an owned version protection operation

     Requires storage manage scope and a bucket read grant. Available with ingress or enrollment
    disabled. Provider identities and worker leases are private.

    Args:
        slug (str):
        bucket (UUID):
        operation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectVersionProtection | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        operation=operation,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    operation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectVersionProtection | Problem]:
    """Inspect an owned version protection operation

     Requires storage manage scope and a bucket read grant. Available with ingress or enrollment
    disabled. Provider identities and worker leases are private.

    Args:
        slug (str):
        bucket (UUID):
        operation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectVersionProtection | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        operation=operation,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    operation: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectVersionProtection | Problem | None:
    """Inspect an owned version protection operation

     Requires storage manage scope and a bucket read grant. Available with ingress or enrollment
    disabled. Provider identities and worker leases are private.

    Args:
        slug (str):
        bucket (UUID):
        operation (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectVersionProtection | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            operation=operation,
            client=client,
        )
    ).parsed
