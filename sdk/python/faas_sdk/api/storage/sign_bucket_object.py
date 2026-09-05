from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.object_sign_request import ObjectSignRequest
from ...models.object_signed_request import ObjectSignedRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    bucket: UUID,
    *,
    body: ObjectSignRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/buckets/{bucket}/signed-url".format(
            slug=quote(str(slug), safe=""),
            bucket=quote(str(bucket), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectSignedRequest | Problem:
    if response.status_code == 200:
        response_200 = ObjectSignedRequest.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectSignedRequest | Problem]:
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
    body: ObjectSignRequest,
) -> Response[ObjectSignedRequest | Problem]:
    """Issue a short-lived direct upload or download URL

     GET requires apps:read or admin; PUT requires deploy:write or admin.
    PUT must declare size_bytes, enforced by signed length (or an empty-body
    digest for zero bytes). These reusable bearer URLs expire within 15
    minutes and are not retained by the API idempotency cache. Send only
    returned headers to the URL, never Gregale credentials. In browsers,
    Content-Length is set by fetch from the File body, not manually.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectSignRequest): Exact object operation to authorize for a short time.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectSignedRequest | Problem]
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
    body: ObjectSignRequest,
) -> ObjectSignedRequest | Problem | None:
    """Issue a short-lived direct upload or download URL

     GET requires apps:read or admin; PUT requires deploy:write or admin.
    PUT must declare size_bytes, enforced by signed length (or an empty-body
    digest for zero bytes). These reusable bearer URLs expire within 15
    minutes and are not retained by the API idempotency cache. Send only
    returned headers to the URL, never Gregale credentials. In browsers,
    Content-Length is set by fetch from the File body, not manually.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectSignRequest): Exact object operation to authorize for a short time.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectSignedRequest | Problem
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
    body: ObjectSignRequest,
) -> Response[ObjectSignedRequest | Problem]:
    """Issue a short-lived direct upload or download URL

     GET requires apps:read or admin; PUT requires deploy:write or admin.
    PUT must declare size_bytes, enforced by signed length (or an empty-body
    digest for zero bytes). These reusable bearer URLs expire within 15
    minutes and are not retained by the API idempotency cache. Send only
    returned headers to the URL, never Gregale credentials. In browsers,
    Content-Length is set by fetch from the File body, not manually.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectSignRequest): Exact object operation to authorize for a short time.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectSignedRequest | Problem]
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
    body: ObjectSignRequest,
) -> ObjectSignedRequest | Problem | None:
    """Issue a short-lived direct upload or download URL

     GET requires apps:read or admin; PUT requires deploy:write or admin.
    PUT must declare size_bytes, enforced by signed length (or an empty-body
    digest for zero bytes). These reusable bearer URLs expire within 15
    minutes and are not retained by the API idempotency cache. Send only
    returned headers to the URL, never Gregale credentials. In browsers,
    Content-Length is set by fetch from the File body, not manually.

    Args:
        slug (str):
        bucket (UUID):
        body (ObjectSignRequest): Exact object operation to authorize for a short time.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectSignedRequest | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            bucket=bucket,
            client=client,
            body=body,
        )
    ).parsed
