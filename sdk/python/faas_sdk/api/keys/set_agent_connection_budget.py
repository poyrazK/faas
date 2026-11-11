from http import HTTPStatus
from typing import Any
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.agent_budget_request import AgentBudgetRequest
from ...models.agent_budget_response import AgentBudgetResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    id: str,
    *,
    body: AgentBudgetRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/agent-connections/{id}/budget".format(
            id=quote(str(id), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AgentBudgetResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AgentBudgetResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AgentBudgetResponse | Problem]:
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
    body: AgentBudgetRequest,
) -> Response[AgentBudgetResponse | Problem]:
    """Set a connected agent's budget.

     Sets the agent's monthly budget, makes it unlimited (an organization's
    ceiling still applies) or returns it to the plan's default: half the
    plan's default spend cap. Needs an admin credential; an agent cannot
    change its own budget.

    Args:
        id (str):
        body (AgentBudgetRequest): Sets an agent connection's budget: exactly one of
            monthly_cents, unlimited or default.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentBudgetResponse | Problem]
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
    body: AgentBudgetRequest,
) -> AgentBudgetResponse | Problem | None:
    """Set a connected agent's budget.

     Sets the agent's monthly budget, makes it unlimited (an organization's
    ceiling still applies) or returns it to the plan's default: half the
    plan's default spend cap. Needs an admin credential; an agent cannot
    change its own budget.

    Args:
        id (str):
        body (AgentBudgetRequest): Sets an agent connection's budget: exactly one of
            monthly_cents, unlimited or default.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentBudgetResponse | Problem
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
    body: AgentBudgetRequest,
) -> Response[AgentBudgetResponse | Problem]:
    """Set a connected agent's budget.

     Sets the agent's monthly budget, makes it unlimited (an organization's
    ceiling still applies) or returns it to the plan's default: half the
    plan's default spend cap. Needs an admin credential; an agent cannot
    change its own budget.

    Args:
        id (str):
        body (AgentBudgetRequest): Sets an agent connection's budget: exactly one of
            monthly_cents, unlimited or default.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentBudgetResponse | Problem]
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
    body: AgentBudgetRequest,
) -> AgentBudgetResponse | Problem | None:
    """Set a connected agent's budget.

     Sets the agent's monthly budget, makes it unlimited (an organization's
    ceiling still applies) or returns it to the plan's default: half the
    plan's default spend cap. Needs an admin credential; an agent cannot
    change its own budget.

    Args:
        id (str):
        body (AgentBudgetRequest): Sets an agent connection's budget: exactly one of
            monthly_cents, unlimited or default.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentBudgetResponse | Problem
    """

    return (
        await asyncio_detailed(
            id=id,
            client=client,
            body=body,
        )
    ).parsed
