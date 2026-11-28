from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.agent_undo_plan import AgentUndoPlan
from ...models.agent_undo_request import AgentUndoRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: AgentUndoRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/agent-connections/self/undo/plan",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AgentUndoPlan | Problem | None:
    if response.status_code == 200:
        response_200 = AgentUndoPlan.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AgentUndoPlan | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AgentUndoRequest,
) -> Response[AgentUndoPlan | Problem]:
    """Plan undoing the calling agent's own changes.

     An agent key plans an undo of its own changes, like a person's plan.
    Applying it needs a person's approval.

    Args:
        body (AgentUndoRequest): Chooses an agent's changes to undo: those since a time (default
            the last 24 hours), or the activity entries named.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentUndoPlan | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: AgentUndoRequest,
) -> AgentUndoPlan | Problem | None:
    """Plan undoing the calling agent's own changes.

     An agent key plans an undo of its own changes, like a person's plan.
    Applying it needs a person's approval.

    Args:
        body (AgentUndoRequest): Chooses an agent's changes to undo: those since a time (default
            the last 24 hours), or the activity entries named.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentUndoPlan | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AgentUndoRequest,
) -> Response[AgentUndoPlan | Problem]:
    """Plan undoing the calling agent's own changes.

     An agent key plans an undo of its own changes, like a person's plan.
    Applying it needs a person's approval.

    Args:
        body (AgentUndoRequest): Chooses an agent's changes to undo: those since a time (default
            the last 24 hours), or the activity entries named.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentUndoPlan | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: AgentUndoRequest,
) -> AgentUndoPlan | Problem | None:
    """Plan undoing the calling agent's own changes.

     An agent key plans an undo of its own changes, like a person's plan.
    Applying it needs a person's approval.

    Args:
        body (AgentUndoRequest): Chooses an agent's changes to undo: those since a time (default
            the last 24 hours), or the activity entries named.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentUndoPlan | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
