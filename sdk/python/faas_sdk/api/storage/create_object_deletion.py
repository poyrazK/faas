from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_deletion import ObjectDeletion
from ...models.object_deletion_request import ObjectDeletionRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: ObjectDeletionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/buckets/{bucket}/objects/deletions".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectDeletion | Problem:
    if response.status_code == 200:
        response_200 = ObjectDeletion.from_dict(response.json())

        return response_200

    if response.status_code == 202:
        response_202 = ObjectDeletion.from_dict(response.json())

        return response_202

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
    *,
    client: AuthenticatedClient | Client,
    body: ObjectDeletionRequest,
) -> Response[ObjectDeletion | Problem]:
    """Delete the current object or mutable null version with a durable receipt

     Requires storage write scope and the bucket write grant. Reuse the request ID with the same key and
    selector for retries. Each intent dispatches at most once. Enabled buckets create an accounted
    delete marker. Pending attempts fence bucket writes and configuration until positive proof; elapsed
    time and absence never settle a dispatched mutation.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectDeletionRequest): One durable mutation; reuse the ID for retries of the same
            key and selector.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectDeletion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        body=body,
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
    body: ObjectDeletionRequest,
) -> ObjectDeletion | Problem | None:
    """Delete the current object or mutable null version with a durable receipt

     Requires storage write scope and the bucket write grant. Reuse the request ID with the same key and
    selector for retries. Each intent dispatches at most once. Enabled buckets create an accounted
    delete marker. Pending attempts fence bucket writes and configuration until positive proof; elapsed
    time and absence never settle a dispatched mutation.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectDeletionRequest): One durable mutation; reuse the ID for retries of the same
            key and selector.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectDeletion | Problem
    """

    return sync_detailed(
        slug=slug,
        bucket=bucket,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectDeletionRequest,
) -> Response[ObjectDeletion | Problem]:
    """Delete the current object or mutable null version with a durable receipt

     Requires storage write scope and the bucket write grant. Reuse the request ID with the same key and
    selector for retries. Each intent dispatches at most once. Enabled buckets create an accounted
    delete marker. Pending attempts fence bucket writes and configuration until positive proof; elapsed
    time and absence never settle a dispatched mutation.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectDeletionRequest): One durable mutation; reuse the ID for retries of the same
            key and selector.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectDeletion | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        bucket=bucket,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    bucket: UUID,
    *,
    client: AuthenticatedClient | Client,
    body: ObjectDeletionRequest,
) -> ObjectDeletion | Problem | None:
    """Delete the current object or mutable null version with a durable receipt

     Requires storage write scope and the bucket write grant. Reuse the request ID with the same key and
    selector for retries. Each intent dispatches at most once. Enabled buckets create an accounted
    delete marker. Pending attempts fence bucket writes and configuration until positive proof; elapsed
    time and absence never settle a dispatched mutation.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectDeletionRequest): One durable mutation; reuse the ID for retries of the same
            key and selector.

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
            client=client,
            body=body,
        )
    ).parsed
