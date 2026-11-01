from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.agent_connections_revoke_all_response import AgentConnectionsRevokeAllResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/agent-connections/revoke_all",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AgentConnectionsRevokeAllResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AgentConnectionsRevokeAllResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AgentConnectionsRevokeAllResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AgentConnectionsRevokeAllResponse | Problem]:
    """Revoke every agent connected to the account.

     Revokes the API key of every connected agent; each must sign in
    again. Needs an admin credential. Audits `key.revoked` with reason
    `agent_disconnected` for each key and `agent_connections.revoke_all`
    with the count.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentConnectionsRevokeAllResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> AgentConnectionsRevokeAllResponse | Problem | None:
    """Revoke every agent connected to the account.

     Revokes the API key of every connected agent; each must sign in
    again. Needs an admin credential. Audits `key.revoked` with reason
    `agent_disconnected` for each key and `agent_connections.revoke_all`
    with the count.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentConnectionsRevokeAllResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AgentConnectionsRevokeAllResponse | Problem]:
    """Revoke every agent connected to the account.

     Revokes the API key of every connected agent; each must sign in
    again. Needs an admin credential. Audits `key.revoked` with reason
    `agent_disconnected` for each key and `agent_connections.revoke_all`
    with the count.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentConnectionsRevokeAllResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> AgentConnectionsRevokeAllResponse | Problem | None:
    """Revoke every agent connected to the account.

     Revokes the API key of every connected agent; each must sign in
    again. Needs an admin credential. Audits `key.revoked` with reason
    `agent_disconnected` for each key and `agent_connections.revoke_all`
    with the count.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentConnectionsRevokeAllResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
