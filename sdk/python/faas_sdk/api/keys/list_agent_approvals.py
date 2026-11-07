from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.agent_approvals_response import AgentApprovalsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/agent-approvals",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AgentApprovalsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AgentApprovalsResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AgentApprovalsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AgentApprovalsResponse | Problem]:
    """List agent approval requests.

     With an agent connection's key: that agent's requests that needed a
    person's approval, newest first. With any other credential: the
    pending requests the caller may decide, those of agents they
    connected and of agents acting in organizations they own or
    administer. A person decides only on the review page.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentApprovalsResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> AgentApprovalsResponse | Problem | None:
    """List agent approval requests.

     With an agent connection's key: that agent's requests that needed a
    person's approval, newest first. With any other credential: the
    pending requests the caller may decide, those of agents they
    connected and of agents acting in organizations they own or
    administer. A person decides only on the review page.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentApprovalsResponse | Problem
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[AgentApprovalsResponse | Problem]:
    """List agent approval requests.

     With an agent connection's key: that agent's requests that needed a
    person's approval, newest first. With any other credential: the
    pending requests the caller may decide, those of agents they
    connected and of agents acting in organizations they own or
    administer. A person decides only on the review page.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentApprovalsResponse | Problem]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> AgentApprovalsResponse | Problem | None:
    """List agent approval requests.

     With an agent connection's key: that agent's requests that needed a
    person's approval, newest first. With any other credential: the
    pending requests the caller may decide, those of agents they
    connected and of agents acting in organizations they own or
    administer. A person decides only on the review page.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentApprovalsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
