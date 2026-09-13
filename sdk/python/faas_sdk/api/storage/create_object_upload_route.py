from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.create_object_upload_route_request import CreateObjectUploadRouteRequest
from ...models.object_upload_route import ObjectUploadRoute
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: CreateObjectUploadRouteRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/upload-routes".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ObjectUploadRoute | Problem:
    if response.status_code == 200:
        response_200 = ObjectUploadRoute.from_dict(response.json())

        return response_200

    if response.status_code == 201:
        response_201 = ObjectUploadRoute.from_dict(response.json())

        return response_201

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ObjectUploadRoute | Problem]:
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
    body: CreateObjectUploadRouteRequest,
) -> Response[ObjectUploadRoute | Problem]:
    """Create or update an authenticated upload route

     Declares POST /uploads/{name}. Gregale authenticates an API key, generates an owner-scoped object
    key, enforces the byte/content-type policy, streams directly to the selected provider, and records a
    completion receipt.

    Args:
        slug (str):
        body (CreateObjectUploadRouteRequest): Upload policy declaration for POST /uploads/{name}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectUploadRoute | Problem]
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
    body: CreateObjectUploadRouteRequest,
) -> ObjectUploadRoute | Problem | None:
    """Create or update an authenticated upload route

     Declares POST /uploads/{name}. Gregale authenticates an API key, generates an owner-scoped object
    key, enforces the byte/content-type policy, streams directly to the selected provider, and records a
    completion receipt.

    Args:
        slug (str):
        body (CreateObjectUploadRouteRequest): Upload policy declaration for POST /uploads/{name}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectUploadRoute | Problem
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
    body: CreateObjectUploadRouteRequest,
) -> Response[ObjectUploadRoute | Problem]:
    """Create or update an authenticated upload route

     Declares POST /uploads/{name}. Gregale authenticates an API key, generates an owner-scoped object
    key, enforces the byte/content-type policy, streams directly to the selected provider, and records a
    completion receipt.

    Args:
        slug (str):
        body (CreateObjectUploadRouteRequest): Upload policy declaration for POST /uploads/{name}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ObjectUploadRoute | Problem]
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
    body: CreateObjectUploadRouteRequest,
) -> ObjectUploadRoute | Problem | None:
    """Create or update an authenticated upload route

     Declares POST /uploads/{name}. Gregale authenticates an API key, generates an owner-scoped object
    key, enforces the byte/content-type policy, streams directly to the selected provider, and records a
    completion receipt.

    Args:
        slug (str):
        body (CreateObjectUploadRouteRequest): Upload policy declaration for POST /uploads/{name}.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ObjectUploadRoute | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
