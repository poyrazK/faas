from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.invoke_request import InvokeRequest
from ...models.invoke_response import InvokeResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: InvokeRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/invoke".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> InvokeResponse | Problem | None:
    if response.status_code == 200:
        response_200 = InvokeResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 413:
        response_413 = Problem.from_dict(response.json())

        return response_413

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 504:
        response_504 = Problem.from_dict(response.json())

        return response_504

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[InvokeResponse | Problem]:
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
    body: InvokeRequest,
) -> Response[InvokeResponse | Problem]:
    """Sync-invoke an app; long-poll for the result.

     Enqueues an invocation row and waits for the drain to drive it
    to a terminal state. Server-side cap is 30s on paid plans, 5s
    on Free. Returns 504 (long_poll_timeout) when the cap elapses;
    the customer can immediately re-call /v1/invocations/{id}
    to pick up the eventual result.

    Args:
        slug (str):
        body (InvokeRequest): Body for POST /v1/apps/{slug}/invoke[/async]. Method defaults to
            POST; path defaults to `/`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvokeResponse | Problem]
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
    body: InvokeRequest,
) -> InvokeResponse | Problem | None:
    """Sync-invoke an app; long-poll for the result.

     Enqueues an invocation row and waits for the drain to drive it
    to a terminal state. Server-side cap is 30s on paid plans, 5s
    on Free. Returns 504 (long_poll_timeout) when the cap elapses;
    the customer can immediately re-call /v1/invocations/{id}
    to pick up the eventual result.

    Args:
        slug (str):
        body (InvokeRequest): Body for POST /v1/apps/{slug}/invoke[/async]. Method defaults to
            POST; path defaults to `/`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvokeResponse | Problem
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
    body: InvokeRequest,
) -> Response[InvokeResponse | Problem]:
    """Sync-invoke an app; long-poll for the result.

     Enqueues an invocation row and waits for the drain to drive it
    to a terminal state. Server-side cap is 30s on paid plans, 5s
    on Free. Returns 504 (long_poll_timeout) when the cap elapses;
    the customer can immediately re-call /v1/invocations/{id}
    to pick up the eventual result.

    Args:
        slug (str):
        body (InvokeRequest): Body for POST /v1/apps/{slug}/invoke[/async]. Method defaults to
            POST; path defaults to `/`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[InvokeResponse | Problem]
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
    body: InvokeRequest,
) -> InvokeResponse | Problem | None:
    """Sync-invoke an app; long-poll for the result.

     Enqueues an invocation row and waits for the drain to drive it
    to a terminal state. Server-side cap is 30s on paid plans, 5s
    on Free. Returns 504 (long_poll_timeout) when the cap elapses;
    the customer can immediately re-call /v1/invocations/{id}
    to pick up the eventual result.

    Args:
        slug (str):
        body (InvokeRequest): Body for POST /v1/apps/{slug}/invoke[/async]. Method defaults to
            POST; path defaults to `/`.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        InvokeResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
