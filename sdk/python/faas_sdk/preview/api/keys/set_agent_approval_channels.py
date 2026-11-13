from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.agent_approval_channels_request import AgentApprovalChannelsRequest
from ...models.agent_approval_channels_response import AgentApprovalChannelsResponse
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: AgentApprovalChannelsRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "put",
        "url": "/v1/agent-approvals/channels",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> AgentApprovalChannelsResponse | Problem | None:
    if response.status_code == 200:
        response_200 = AgentApprovalChannelsResponse.from_dict(response.json())

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

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[AgentApprovalChannelsResponse | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AgentApprovalChannelsRequest,
) -> Response[AgentApprovalChannelsResponse | Problem]:
    """Choose where the account's agents' approval requests also go.

     Replaces the notification channels that get the account's agents'
    approval requests. Name your own channels; an empty list leaves
    email only. Needs an admin credential, so an agent cannot change
    where its own requests go.

    Args:
        body (AgentApprovalChannelsRequest): The notification channels to send the account's
            agents' approval requests to.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentApprovalChannelsResponse | Problem]
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
    body: AgentApprovalChannelsRequest,
) -> AgentApprovalChannelsResponse | Problem | None:
    """Choose where the account's agents' approval requests also go.

     Replaces the notification channels that get the account's agents'
    approval requests. Name your own channels; an empty list leaves
    email only. Needs an admin credential, so an agent cannot change
    where its own requests go.

    Args:
        body (AgentApprovalChannelsRequest): The notification channels to send the account's
            agents' approval requests to.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentApprovalChannelsResponse | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: AgentApprovalChannelsRequest,
) -> Response[AgentApprovalChannelsResponse | Problem]:
    """Choose where the account's agents' approval requests also go.

     Replaces the notification channels that get the account's agents'
    approval requests. Name your own channels; an empty list leaves
    email only. Needs an admin credential, so an agent cannot change
    where its own requests go.

    Args:
        body (AgentApprovalChannelsRequest): The notification channels to send the account's
            agents' approval requests to.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[AgentApprovalChannelsResponse | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: AgentApprovalChannelsRequest,
) -> AgentApprovalChannelsResponse | Problem | None:
    """Choose where the account's agents' approval requests also go.

     Replaces the notification channels that get the account's agents'
    approval requests. Name your own channels; an empty list leaves
    email only. Needs an admin credential, so an agent cannot change
    where its own requests go.

    Args:
        body (AgentApprovalChannelsRequest): The notification channels to send the account's
            agents' approval requests to.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        AgentApprovalChannelsResponse | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
