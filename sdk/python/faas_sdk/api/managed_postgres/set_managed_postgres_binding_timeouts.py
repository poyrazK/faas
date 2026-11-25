from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ...client import AuthenticatedClient, Client
from ...models.managed_postgres_binding_timeouts import ManagedPostgresBindingTimeouts
from ...models.problem import Problem
from ...models.set_managed_postgres_binding_timeouts_request import SetManagedPostgresBindingTimeoutsRequest
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: SetManagedPostgresBindingTimeoutsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/postgres/bindings/{id}/timeouts".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ManagedPostgresBindingTimeouts | Problem:
    if response.status_code == 200:
        response_200 = ManagedPostgresBindingTimeouts.from_dict(response.json())

        return response_200

    response_default = Problem.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ManagedPostgresBindingTimeouts | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresBindingTimeoutsRequest,
) -> Response[ManagedPostgresBindingTimeouts | Problem]:
    """Save timeout defaults for a native pooled app binding.

     Changes the supplied settings on a ready read_write or read_only binding. Zero disables a setting.
    Application happens asynchronously and existing transactions keep their current defaults.

    Args:
        id (str):
        body (SetManagedPostgresBindingTimeoutsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresBindingTimeouts | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresBindingTimeoutsRequest,
) -> ManagedPostgresBindingTimeouts | Problem | None:
    """Save timeout defaults for a native pooled app binding.

     Changes the supplied settings on a ready read_write or read_only binding. Zero disables a setting.
    Application happens asynchronously and existing transactions keep their current defaults.

    Args:
        id (str):
        body (SetManagedPostgresBindingTimeoutsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresBindingTimeouts | Problem
    """

    return sync_detailed(
        id=id,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresBindingTimeoutsRequest,
) -> Response[ManagedPostgresBindingTimeouts | Problem]:
    """Save timeout defaults for a native pooled app binding.

     Changes the supplied settings on a ready read_write or read_only binding. Zero disables a setting.
    Application happens asynchronously and existing transactions keep their current defaults.

    Args:
        id (str):
        body (SetManagedPostgresBindingTimeoutsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ManagedPostgresBindingTimeouts | Problem]
    """

    kwargs = _get_kwargs(
        id=id,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: SetManagedPostgresBindingTimeoutsRequest,
) -> ManagedPostgresBindingTimeouts | Problem | None:
    """Save timeout defaults for a native pooled app binding.

     Changes the supplied settings on a ready read_write or read_only binding. Zero disables a setting.
    Application happens asynchronously and existing transactions keep their current defaults.

    Args:
        id (str):
        body (SetManagedPostgresBindingTimeoutsRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ManagedPostgresBindingTimeouts | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
