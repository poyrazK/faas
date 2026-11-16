from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.agent_undo_plan import AgentUndoPlan
from ...models.agent_undo_request import AgentUndoRequest
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: AgentUndoRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/agent-connections/{id}/undo/plan".format(
            id=quote(str(id), safe=""),
        ),
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

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

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
    id: str,
    *,
    client: AuthenticatedClient | Client,
    body: AgentUndoRequest,
) -> Response[AgentUndoPlan | Problem]:
    """Plan undoing a connected agent's changes.

     Lists the agent's changes the request selects, newest first (by
    default those of the last 24 hours; changes are kept 7 days), with
    what undoing each will do: undo it, leave it because it cannot be
    undone or because someone changed it again since, or nothing because
    it was already undone. Apply the plan with its plan_digest. An agent
    key cannot plan or apply an undo.

    Args:
        id (str):
        body (AgentUndoRequest): Chooses an agent's changes to undo: those since a time (default
            the last 24 hours), or the activity entries named.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentUndoPlan | Problem]
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
    body: AgentUndoRequest,
) -> AgentUndoPlan | Problem | None:
    """Plan undoing a connected agent's changes.

     Lists the agent's changes the request selects, newest first (by
    default those of the last 24 hours; changes are kept 7 days), with
    what undoing each will do: undo it, leave it because it cannot be
    undone or because someone changed it again since, or nothing because
    it was already undone. Apply the plan with its plan_digest. An agent
    key cannot plan or apply an undo.

    Args:
        id (str):
        body (AgentUndoRequest): Chooses an agent's changes to undo: those since a time (default
            the last 24 hours), or the activity entries named.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentUndoPlan | Problem
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
    body: AgentUndoRequest,
) -> Response[AgentUndoPlan | Problem]:
    """Plan undoing a connected agent's changes.

     Lists the agent's changes the request selects, newest first (by
    default those of the last 24 hours; changes are kept 7 days), with
    what undoing each will do: undo it, leave it because it cannot be
    undone or because someone changed it again since, or nothing because
    it was already undone. Apply the plan with its plan_digest. An agent
    key cannot plan or apply an undo.

    Args:
        id (str):
        body (AgentUndoRequest): Chooses an agent's changes to undo: those since a time (default
            the last 24 hours), or the activity entries named.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentUndoPlan | Problem]
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
    body: AgentUndoRequest,
) -> AgentUndoPlan | Problem | None:
    """Plan undoing a connected agent's changes.

     Lists the agent's changes the request selects, newest first (by
    default those of the last 24 hours; changes are kept 7 days), with
    what undoing each will do: undo it, leave it because it cannot be
    undone or because someone changed it again since, or nothing because
    it was already undone. Apply the plan with its plan_digest. An agent
    key cannot plan or apply an undo.

    Args:
        id (str):
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
            id=id,
            client=client,
            body=body,
        )
    ).parsed
