from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.create_object_bucket_body import CreateObjectBucketBody
from ...models.object_bucket import ObjectBucket
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateObjectBucketBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/buckets".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectBucket | Problem:
    if response.status_code == 200:
        response_200 = ObjectBucket.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = ObjectBucket.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectBucket | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateObjectBucketBody,
) -> Response[ObjectBucket | Problem]:
    """Create a private bucket on the region's current default backend

     Requires storage:manage or admin. Idempotent by app, scope and name, not
    by Idempotency-Key. Retry provisioning by submitting the same name and
    scope. Existing buckets retain their backend when the default changes.

    Args:
        slug (str):
        body (CreateObjectBucketBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucket | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateObjectBucketBody,
) -> ObjectBucket | Problem | None:
    """Create a private bucket on the region's current default backend

     Requires storage:manage or admin. Idempotent by app, scope and name, not
    by Idempotency-Key. Retry provisioning by submitting the same name and
    scope. Existing buckets retain their backend when the default changes.

    Args:
        slug (str):
        body (CreateObjectBucketBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucket | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateObjectBucketBody,
) -> Response[ObjectBucket | Problem]:
    """Create a private bucket on the region's current default backend

     Requires storage:manage or admin. Idempotent by app, scope and name, not
    by Idempotency-Key. Retry provisioning by submitting the same name and
    scope. Existing buckets retain their backend when the default changes.

    Args:
        slug (str):
        body (CreateObjectBucketBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectBucket | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateObjectBucketBody,
) -> ObjectBucket | Problem | None:
    """Create a private bucket on the region's current default backend

     Requires storage:manage or admin. Idempotent by app, scope and name, not
    by Idempotency-Key. Retry provisioning by submitting the same name and
    scope. Existing buckets retain their backend when the default changes.

    Args:
        slug (str):
        body (CreateObjectBucketBody):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectBucket | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
