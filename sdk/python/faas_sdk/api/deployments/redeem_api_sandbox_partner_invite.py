from http import HTTPStatus
from typing import Any
from urllib.parse import quote
from uuid import UUID

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.api_sandbox_invite_response import APISandboxInviteResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    sandbox: UUID,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/api-sandboxes/{sandbox}/redeem".format(
            sandbox=quote(str(sandbox), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> APISandboxInviteResponse | Problem | None:
    if response.status_code == 200:
        response_200 = APISandboxInviteResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[APISandboxInviteResponse | Problem]:
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
) -> Response[APISandboxInviteResponse | Problem]:
    """Exchange a one-time partner invite for a sandbox key.

     Supply the invite token in the Authorization bearer header. The key and recipient guide are returned
    once after sandbox verification.

    Args:
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxInviteResponse | Problem]
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
) -> APISandboxInviteResponse | Problem | None:
    """Exchange a one-time partner invite for a sandbox key.

     Supply the invite token in the Authorization bearer header. The key and recipient guide are returned
    once after sandbox verification.

    Args:
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxInviteResponse | Problem
    """

    return sync_detailed(
        sandbox=sandbox,
        client=client,
    ).parsed


async def asyncio_detailed(
    sandbox: UUID,
    *,
    client: AuthenticatedClient,
) -> Response[APISandboxInviteResponse | Problem]:
    """Exchange a one-time partner invite for a sandbox key.

     Supply the invite token in the Authorization bearer header. The key and recipient guide are returned
    once after sandbox verification.

    Args:
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[APISandboxInviteResponse | Problem]
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
) -> APISandboxInviteResponse | Problem | None:
    """Exchange a one-time partner invite for a sandbox key.

     Supply the invite token in the Authorization bearer header. The key and recipient guide are returned
    once after sandbox verification.

    Args:
        sandbox (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        APISandboxInviteResponse | Problem
    """

    return (
        await asyncio_detailed(
            sandbox=sandbox,
            client=client,
        )
    ).parsed
