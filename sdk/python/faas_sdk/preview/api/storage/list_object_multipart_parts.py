from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_multipart_part_list import ObjectMultipartPartList
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    part_number_marker: int | Unset = 0,
    limit: int | Unset = 1000,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["part_number_marker"] = part_number_marker

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/multipart-uploads/{upload}/parts".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            upload=quote(str(upload), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectMultipartPartList | Problem:
    if response.status_code == 200:
        response_200 = ObjectMultipartPartList.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectMultipartPartList | Problem]:
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
    part_number_marker: int | Unset = 0,
    limit: int | Unset = 1000,
) -> Response[ObjectMultipartPartList | Problem]:
    """List parts confirmed by the storage provider

     Requires storage:write or admin and a matching bucket grant. Returns provider-confirmed ETags so an
    interrupted client can resume completion without exposing provider credentials or upload IDs.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        part_number_marker (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 1000.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectMultipartPartList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        upload=upload,
        part_number_marker=part_number_marker,
        limit=limit,
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
    part_number_marker: int | Unset = 0,
    limit: int | Unset = 1000,
) -> ObjectMultipartPartList | Problem | None:
    """List parts confirmed by the storage provider

     Requires storage:write or admin and a matching bucket grant. Returns provider-confirmed ETags so an
    interrupted client can resume completion without exposing provider credentials or upload IDs.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        part_number_marker (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 1000.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectMultipartPartList | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        upload=upload,
        client=client,
        part_number_marker=part_number_marker,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    client: AuthenticatedClient | Client,
    part_number_marker: int | Unset = 0,
    limit: int | Unset = 1000,
) -> Response[ObjectMultipartPartList | Problem]:
    """List parts confirmed by the storage provider

     Requires storage:write or admin and a matching bucket grant. Returns provider-confirmed ETags so an
    interrupted client can resume completion without exposing provider credentials or upload IDs.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        part_number_marker (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 1000.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectMultipartPartList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        upload=upload,
        part_number_marker=part_number_marker,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    upload: UUID,
    *,
    client: AuthenticatedClient | Client,
    part_number_marker: int | Unset = 0,
    limit: int | Unset = 1000,
) -> ObjectMultipartPartList | Problem | None:
    """List parts confirmed by the storage provider

     Requires storage:write or admin and a matching bucket grant. Returns provider-confirmed ETags so an
    interrupted client can resume completion without exposing provider credentials or upload IDs.

    Args:
        slug (str):
        bucket (UUID):
        upload (UUID):
        part_number_marker (int | Unset):  Default: 0.
        limit (int | Unset):  Default: 1000.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectMultipartPartList | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            upload=upload,
            client=client,
            part_number_marker=part_number_marker,
            limit=limit,
        )
    ).parsed
