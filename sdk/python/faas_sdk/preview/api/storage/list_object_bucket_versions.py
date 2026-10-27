from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_version_list import ObjectVersionList
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    prefix: str | Unset = UNSET,
    delimiter: str | Unset = UNSET,
    limit: int | Unset = 1000,
    key_marker: str | Unset = UNSET,
    version_id_marker: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["prefix"] = prefix

    params["delimiter"] = delimiter

    params["limit"] = limit

    params["key_marker"] = key_marker

    params["version_id_marker"] = version_id_marker

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/objects/versions".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectVersionList | Problem:
    if response.status_code == 200:
        response_200 = ObjectVersionList.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectVersionList | Problem]:
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
    prefix: str | Unset = UNSET,
    delimiter: str | Unset = UNSET,
    limit: int | Unset = 1000,
    key_marker: str | Unset = UNSET,
    version_id_marker: str | Unset = UNSET,
) -> Response[ObjectVersionList | Problem]:
    """List retained object versions and delete markers

     Requires storage read scope and a bucket read grant. Returns owned public version selectors, never
    native generations or physical placement. Both continuation markers belong to this bucket and exact
    key. Each provider page consumes the existing request safety budget.

    Args:
        slug (str):
        bucket (UUID):
        prefix (str | Unset):
        delimiter (str | Unset):
        limit (int | Unset):  Default: 1000.
        key_marker (str | Unset):
        version_id_marker (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectVersionList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        prefix=prefix,
        delimiter=delimiter,
        limit=limit,
        key_marker=key_marker,
        version_id_marker=version_id_marker,
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
    prefix: str | Unset = UNSET,
    delimiter: str | Unset = UNSET,
    limit: int | Unset = 1000,
    key_marker: str | Unset = UNSET,
    version_id_marker: str | Unset = UNSET,
) -> ObjectVersionList | Problem | None:
    """List retained object versions and delete markers

     Requires storage read scope and a bucket read grant. Returns owned public version selectors, never
    native generations or physical placement. Both continuation markers belong to this bucket and exact
    key. Each provider page consumes the existing request safety budget.

    Args:
        slug (str):
        bucket (UUID):
        prefix (str | Unset):
        delimiter (str | Unset):
        limit (int | Unset):  Default: 1000.
        key_marker (str | Unset):
        version_id_marker (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectVersionList | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
        prefix=prefix,
        delimiter=delimiter,
        limit=limit,
        key_marker=key_marker,
        version_id_marker=version_id_marker,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    prefix: str | Unset = UNSET,
    delimiter: str | Unset = UNSET,
    limit: int | Unset = 1000,
    key_marker: str | Unset = UNSET,
    version_id_marker: str | Unset = UNSET,
) -> Response[ObjectVersionList | Problem]:
    """List retained object versions and delete markers

     Requires storage read scope and a bucket read grant. Returns owned public version selectors, never
    native generations or physical placement. Both continuation markers belong to this bucket and exact
    key. Each provider page consumes the existing request safety budget.

    Args:
        slug (str):
        bucket (UUID):
        prefix (str | Unset):
        delimiter (str | Unset):
        limit (int | Unset):  Default: 1000.
        key_marker (str | Unset):
        version_id_marker (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectVersionList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        prefix=prefix,
        delimiter=delimiter,
        limit=limit,
        key_marker=key_marker,
        version_id_marker=version_id_marker,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    prefix: str | Unset = UNSET,
    delimiter: str | Unset = UNSET,
    limit: int | Unset = 1000,
    key_marker: str | Unset = UNSET,
    version_id_marker: str | Unset = UNSET,
) -> ObjectVersionList | Problem | None:
    """List retained object versions and delete markers

     Requires storage read scope and a bucket read grant. Returns owned public version selectors, never
    native generations or physical placement. Both continuation markers belong to this bucket and exact
    key. Each provider page consumes the existing request safety budget.

    Args:
        slug (str):
        bucket (UUID):
        prefix (str | Unset):
        delimiter (str | Unset):
        limit (int | Unset):  Default: 1000.
        key_marker (str | Unset):
        version_id_marker (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectVersionList | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            prefix=prefix,
            delimiter=delimiter,
            limit=limit,
            key_marker=key_marker,
            version_id_marker=version_id_marker,
        )
    ).parsed
