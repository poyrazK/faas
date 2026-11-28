from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_lifecycle_scan import ObjectLifecycleScan
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    scan: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/lifecycle/scans/{scan}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            scan=quote(str(scan), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectLifecycleScan | Problem:
    if response.status_code == 200:
        response_200 = ObjectLifecycleScan.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectLifecycleScan | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    scan: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectLifecycleScan | Problem]:
    """Read lifecycle discovery progress

     Requires storage manage scope and the bucket write grant. Returns owned persisted progress without
    provider credentials, native identifiers, cursors or lease tokens. Available while ingress is
    disabled.

    Args:
        slug (str):
        bucket (UUID):
        scan (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectLifecycleScan | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        scan=scan,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    scan: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectLifecycleScan | Problem | None:
    """Read lifecycle discovery progress

     Requires storage manage scope and the bucket write grant. Returns owned persisted progress without
    provider credentials, native identifiers, cursors or lease tokens. Available while ingress is
    disabled.

    Args:
        slug (str):
        bucket (UUID):
        scan (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectLifecycleScan | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        scan=scan,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    scan: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectLifecycleScan | Problem]:
    """Read lifecycle discovery progress

     Requires storage manage scope and the bucket write grant. Returns owned persisted progress without
    provider credentials, native identifiers, cursors or lease tokens. Available while ingress is
    disabled.

    Args:
        slug (str):
        bucket (UUID):
        scan (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectLifecycleScan | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        scan=scan,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    scan: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectLifecycleScan | Problem | None:
    """Read lifecycle discovery progress

     Requires storage manage scope and the bucket write grant. Returns owned persisted progress without
    provider credentials, native identifiers, cursors or lease tokens. Available while ingress is
    disabled.

    Args:
        slug (str):
        bucket (UUID):
        scan (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectLifecycleScan | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            scan=scan,
            client=client,
        )
    ).parsed
