from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.list_object_write_receipts_status import (
    ListObjectWriteReceiptsStatus,
)
from ...models.object_write_receipt_list import ObjectWriteReceiptList
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    status: ListObjectWriteReceiptsStatus | Unset = "pending",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_status: str | Unset = UNSET
    if not isinstance(status, Unset):
        json_status = status

    params["status"] = json_status

    params["limit"] = limit

    params["cursor"] = cursor

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/write-receipts".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ObjectWriteReceiptList | Problem:
    if response.status_code == 200:
        response_200 = ObjectWriteReceiptList.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectWriteReceiptList | Problem]:
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
    status: ListObjectWriteReceiptsStatus | Unset = "pending",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> Response[ObjectWriteReceiptList | Problem]:
    """List tracked write receipts for a bucket

     Requires storage write scope and the bucket write grant. Defaults to pending writes, newest first;
    pages are live views and receipts settling between reads may disappear from the pending filter.
    Reads remain available while storage is disabled or budgets are spent, without provider calls or
    quota admission. Direct signed uploads, legacy writes and multipart sessions are outside this
    receipt list.

    Args:
        slug (str):
        bucket (UUID):
        status (ListObjectWriteReceiptsStatus | Unset):  Default: 'pending'.
        limit (int | Unset):  Default: 50.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectWriteReceiptList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        status=status,
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
    status: ListObjectWriteReceiptsStatus | Unset = "pending",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> ObjectWriteReceiptList | Problem | None:
    """List tracked write receipts for a bucket

     Requires storage write scope and the bucket write grant. Defaults to pending writes, newest first;
    pages are live views and receipts settling between reads may disappear from the pending filter.
    Reads remain available while storage is disabled or budgets are spent, without provider calls or
    quota admission. Direct signed uploads, legacy writes and multipart sessions are outside this
    receipt list.

    Args:
        slug (str):
        bucket (UUID):
        status (ListObjectWriteReceiptsStatus | Unset):  Default: 'pending'.
        limit (int | Unset):  Default: 50.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectWriteReceiptList | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
        status=status,
        limit=limit,
        cursor=cursor,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    status: ListObjectWriteReceiptsStatus | Unset = "pending",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> Response[ObjectWriteReceiptList | Problem]:
    """List tracked write receipts for a bucket

     Requires storage write scope and the bucket write grant. Defaults to pending writes, newest first;
    pages are live views and receipts settling between reads may disappear from the pending filter.
    Reads remain available while storage is disabled or budgets are spent, without provider calls or
    quota admission. Direct signed uploads, legacy writes and multipart sessions are outside this
    receipt list.

    Args:
        slug (str):
        bucket (UUID):
        status (ListObjectWriteReceiptsStatus | Unset):  Default: 'pending'.
        limit (int | Unset):  Default: 50.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectWriteReceiptList | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        status=status,
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
    status: ListObjectWriteReceiptsStatus | Unset = "pending",
    limit: int | Unset = 50,
    cursor: str | Unset = UNSET,
) -> ObjectWriteReceiptList | Problem | None:
    """List tracked write receipts for a bucket

     Requires storage write scope and the bucket write grant. Defaults to pending writes, newest first;
    pages are live views and receipts settling between reads may disappear from the pending filter.
    Reads remain available while storage is disabled or budgets are spent, without provider calls or
    quota admission. Direct signed uploads, legacy writes and multipart sessions are outside this
    receipt list.

    Args:
        slug (str):
        bucket (UUID):
        status (ListObjectWriteReceiptsStatus | Unset):  Default: 'pending'.
        limit (int | Unset):  Default: 50.
        cursor (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectWriteReceiptList | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            status=status,
            limit=limit,
            cursor=cursor,
        )
    ).parsed
