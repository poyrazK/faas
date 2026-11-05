from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_multipart_upload_list import ObjectMultipartUploadList
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    limit: int | Unset = 100,
    cursor: UUID | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["limit"] = limit

    json_cursor: str | Unset = UNSET
    if not isinstance(cursor, Unset):
        json_cursor = str(cursor)
    params["cursor"] = json_cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/multipart-uploads".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectMultipartUploadList | Problem:
    if response.status_code == 200:
        response_200 = ObjectMultipartUploadList.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectMultipartUploadList | Problem]:
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
    limit: int | Unset = 100,
    cursor: UUID | Unset = UNSET,
) -> Response[ObjectMultipartUploadList | Problem]:
    """List durable resumable upload sessions

     Requires storage:write or admin and a matching bucket grant. The provider upload ID is never
    returned. Use cursor to recover sessions after a client loses its local session identifier.

    Args:
        slug (str):
        bucket (UUID):
        limit (int | Unset):  Default: 100.
        cursor (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectMultipartUploadList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        limit=limit,
        cursor=cursor,
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
    limit: int | Unset = 100,
    cursor: UUID | Unset = UNSET,
) -> ObjectMultipartUploadList | Problem | None:
    """List durable resumable upload sessions

     Requires storage:write or admin and a matching bucket grant. The provider upload ID is never
    returned. Use cursor to recover sessions after a client loses its local session identifier.

    Args:
        slug (str):
        bucket (UUID):
        limit (int | Unset):  Default: 100.
        cursor (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectMultipartUploadList | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 100,
    cursor: UUID | Unset = UNSET,
) -> Response[ObjectMultipartUploadList | Problem]:
    """List durable resumable upload sessions

     Requires storage:write or admin and a matching bucket grant. The provider upload ID is never
    returned. Use cursor to recover sessions after a client loses its local session identifier.

    Args:
        slug (str):
        bucket (UUID):
        limit (int | Unset):  Default: 100.
        cursor (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectMultipartUploadList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        limit=limit,
        cursor=cursor,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    limit: int | Unset = 100,
    cursor: UUID | Unset = UNSET,
) -> ObjectMultipartUploadList | Problem | None:
    """List durable resumable upload sessions

     Requires storage:write or admin and a matching bucket grant. The provider upload ID is never
    returned. Use cursor to recover sessions after a client loses its local session identifier.

    Args:
        slug (str):
        bucket (UUID):
        limit (int | Unset):  Default: 100.
        cursor (UUID | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectMultipartUploadList | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
