from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_response import APISandboxResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    sandbox: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/api-sandboxes/{sandbox}/invite".format(
            sandbox=quote(str(sandbox), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APISandboxResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    sandbox: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[APISandboxResponse | Problem]:
    """Read an API sandbox invitation.

     Authenticates with the invitation credential and returns the exact sandbox URL and matching OpenAPI
    URL.

    Args:
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxResponse | Problem]
    """

    kwargs = _get_kwargs(
        sandbox=sandbox,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    sandbox: UUID,
    *,
    client: AuthenticatedClient,
) -> APISandboxResponse | Problem | None:
    """Read an API sandbox invitation.

     Authenticates with the invitation credential and returns the exact sandbox URL and matching OpenAPI
    URL.

    Args:
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxResponse | Problem
    """

    return sync_detailed(
        sandbox=sandbox,
        client=client,
    ).parsed


async def asyncio_detailed(
    sandbox: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[APISandboxResponse | Problem]:
    """Read an API sandbox invitation.

     Authenticates with the invitation credential and returns the exact sandbox URL and matching OpenAPI
    URL.

    Args:
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxResponse | Problem]
    """

    kwargs = _get_kwargs(
        sandbox=sandbox,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    sandbox: UUID,
    *,
    client: AuthenticatedClient,
) -> APISandboxResponse | Problem | None:
    """Read an API sandbox invitation.

     Authenticates with the invitation credential and returns the exact sandbox URL and matching OpenAPI
    URL.

    Args:
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxResponse | Problem
    """

    return (
        await asyncio_detailed(
            sandbox=sandbox,
            client=client,
        )
    ).parsed
