from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_write_receipt import ObjectWriteReceipt
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    receipt: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/buckets/{bucket}/write-receipts/{receipt}".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
            receipt=quote(str(receipt), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectWriteReceipt | Problem:
    if response.status_code == 200:
        response_200 = ObjectWriteReceipt.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectWriteReceipt | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    bucket: UUID,
    receipt: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectWriteReceipt | Problem]:
    """Read a tracked write receipt

     Requires storage write scope and the bucket write grant. Completed proves this attempt committed,
    not that the object still has this value. Pending has no confirmed outcome; do not assume failure or
    refund capacity. Reads remain available with storage disabled or spent budgets.

    Args:
        slug (str):
        bucket (UUID):
        receipt (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectWriteReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        receipt=receipt,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    bucket: UUID,
    receipt: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectWriteReceipt | Problem | None:
    """Read a tracked write receipt

     Requires storage write scope and the bucket write grant. Completed proves this attempt committed,
    not that the object still has this value. Pending has no confirmed outcome; do not assume failure or
    refund capacity. Reads remain available with storage disabled or spent budgets.

    Args:
        slug (str):
        bucket (UUID):
        receipt (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectWriteReceipt | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        receipt=receipt,
        client=client,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    receipt: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> Response[ObjectWriteReceipt | Problem]:
    """Read a tracked write receipt

     Requires storage write scope and the bucket write grant. Completed proves this attempt committed,
    not that the object still has this value. Pending has no confirmed outcome; do not assume failure or
    refund capacity. Reads remain available with storage disabled or spent budgets.

    Args:
        slug (str):
        bucket (UUID):
        receipt (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectWriteReceipt | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        receipt=receipt,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    receipt: UUID,
    *,
    client: AuthenticatedClient | Client,
) -> ObjectWriteReceipt | Problem | None:
    """Read a tracked write receipt

     Requires storage write scope and the bucket write grant. Completed proves this attempt committed,
    not that the object still has this value. Pending has no confirmed outcome; do not assume failure or
    refund capacity. Reads remain available with storage disabled or spent budgets.

    Args:
        slug (str):
        bucket (UUID):
        receipt (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectWriteReceipt | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            receipt=receipt,
            client=client,
        )
    ).parsed
