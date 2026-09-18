from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.create_tcp_listener_request import CreateTCPListenerRequest
from ...models.problem import Problem
from ...models.tcp_listener_response import TCPListenerResponse
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    body: CreateTCPListenerRequest,
    idempotency_key: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(idempotency_key, Unset):
        headers["Idempotency-Key"] = idempotency_key

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/tcp-listeners".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Problem | TCPListenerResponse | None:
    if response.status_code == 201:
        response_201 = TCPListenerResponse.from_dict(response.json())

        return response_201

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Problem | TCPListenerResponse]:
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
    body: CreateTCPListenerRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | TCPListenerResponse]:
    """Expose one app port over raw TCP.

     Creates an enabled TCP listener. `public_port` is optional; when it
    is omitted Gregale allocates a free port from the reserved
    40000–49999 range. Public ports remain stable across instance wake,
    migration, and redeployments. `name` and `guest_port` must match an
    explicitly declared TCP listener in the app manifest. Unnamed
    declarations use the deterministic name `tcp-<guest_port>`.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateTCPListenerRequest): Request to expose one workload TCP port.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TCPListenerResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateTCPListenerRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | TCPListenerResponse | None:
    """Expose one app port over raw TCP.

     Creates an enabled TCP listener. `public_port` is optional; when it
    is omitted Gregale allocates a free port from the reserved
    40000–49999 range. Public ports remain stable across instance wake,
    migration, and redeployments. `name` and `guest_port` must match an
    explicitly declared TCP listener in the app manifest. Unnamed
    declarations use the deterministic name `tcp-<guest_port>`.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateTCPListenerRequest): Request to expose one workload TCP port.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TCPListenerResponse
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
        idempotency_key=idempotency_key,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateTCPListenerRequest,
    idempotency_key: str | Unset = UNSET,
) -> Response[Problem | TCPListenerResponse]:
    """Expose one app port over raw TCP.

     Creates an enabled TCP listener. `public_port` is optional; when it
    is omitted Gregale allocates a free port from the reserved
    40000–49999 range. Public ports remain stable across instance wake,
    migration, and redeployments. `name` and `guest_port` must match an
    explicitly declared TCP listener in the app manifest. Unnamed
    declarations use the deterministic name `tcp-<guest_port>`.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateTCPListenerRequest): Request to expose one workload TCP port.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Problem | TCPListenerResponse]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
        idempotency_key=idempotency_key,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: CreateTCPListenerRequest,
    idempotency_key: str | Unset = UNSET,
) -> Problem | TCPListenerResponse | None:
    """Expose one app port over raw TCP.

     Creates an enabled TCP listener. `public_port` is optional; when it
    is omitted Gregale allocates a free port from the reserved
    40000–49999 range. Public ports remain stable across instance wake,
    migration, and redeployments. `name` and `guest_port` must match an
    explicitly declared TCP listener in the app manifest. Unnamed
    declarations use the deterministic name `tcp-<guest_port>`.

    Args:
        slug (str):
        idempotency_key (str | Unset):
        body (CreateTCPListenerRequest): Request to expose one workload TCP port.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Problem | TCPListenerResponse
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
            idempotency_key=idempotency_key,
        )
    ).parsed
